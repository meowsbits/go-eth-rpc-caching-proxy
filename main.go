// package main implements a caching proxy for an ethereum json rpc origin.
//
// SERVER
//
// Only HTTP transport is supported.
// An ORIGIN_URL environment variable configures the origin of proxy,
// and must be provided or the application will panic on start up.
//
// VALIDATION
//
// Requests must be of method POST and have Content-Type: application/json or empty.
// Appropriate calls are enforced; only JSONRPC "call"-type requests are supported.
// Batches are fully supported, but not required. Requests will be responsed to in-kind (single:single, batch:batch).
// A blacklist-style validation feature supports quick responses to methods
// which are assumed to be unavailable (eg. 'admin_.*').
//
// API
//
// Response bodies are always JSONRPC-valid objects.
// Requests with invalid encoding, method, or content types get a 400 status response.
// Requests with invalid JSONRPC schemas get a 200 response, but a JSONRPC error message.
//
// CACHING
//
// Caching is handled at the single-request level (batches are disassembled and reassembled).
// Cacheable requests are keyed on their method and params, concatenated with '/' delimiting.
// The actual key is a sha1 sum of this concatenation.
//

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/patrickmn/go-cache"
	json2 "helloworld/json"
)

const defaultCacheExpiration = 2 * time.Second
const defaultCacheExpirationLong = 60 * time.Second

// Create a cache with a default expiration time of defaultCacheExpiration, and which
// purges expired items every 1 second
var c = cache.New(defaultCacheExpiration, 1*time.Second)

// remote is the parsed form of the global app setting of the proxy origin.
var remote *url.URL

type requestMsgValidation struct {
	fn func(message *json2.JsonrpcMessage) *json2.JsonrpcMessage
}

func mustInitOrigin() {
	var err error
	origin := os.Getenv("ORIGIN_URL")
	remote, err = url.Parse(origin)
	if err != nil {
		panic(err)
	}
}

func init() {
	mustInitOrigin()
}

func main() {

	http.HandleFunc("/", handler2)
	http.HandleFunc("/about", aboutHandler)

	// [START setting_port]
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
		log.Printf("Defaulting to port %s", port)
	}

	log.Printf("Listening on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
	// [END setting_port]
}

var (
	errMsgNotCall        = errors.New("request be valid JSON-RPC Call, must have 'method' annotation")
	errMsgIsNotification = errors.New("request must have 'id' annotation")
	errMsgPubSub         = errors.New("server does not support pubsub services")
	errMsgIsResponse     = errors.New("request should not include 'response' annotation")
)

var requestValidations = []requestMsgValidation{
	{
		fn: func(message *json2.JsonrpcMessage) *json2.JsonrpcMessage {
			if !message.IsCall() {
				em := message.ErrorResponse(errMsgNotCall)
				em.Error.Code = json2.InvalidRequestCode
				return em
			}
			return nil
		},
	},
	{
		fn: func(message *json2.JsonrpcMessage) *json2.JsonrpcMessage {
			if message.IsNotification() {
				em := message.ErrorResponse(errMsgIsNotification)
				em.Error.Code = json2.InvalidRequestCode
				return em
			}
			return nil
		},
	},
	{
		fn: func(message *json2.JsonrpcMessage) *json2.JsonrpcMessage {
			if message.IsSubscribe() || message.IsUnsubscribe() {
				em := message.ErrorResponse(errMsgPubSub)
				em.Error.Code = json2.InvalidRequestCode
				return em
			}
			return nil
		},
	},
	{
		fn: func(message *json2.JsonrpcMessage) *json2.JsonrpcMessage {
			if message.IsResponse() {
				em := message.ErrorResponse(errMsgIsResponse)
				em.Error.Code = json2.InvalidRequestCode
				return em
			}
			return nil
		},
	},
	{
		fn: func(message *json2.JsonrpcMessage) *json2.JsonrpcMessage {
			for _, r := range []*regexp.Regexp{
				regexp.MustCompile(`^admin`),
				regexp.MustCompile(`^personal`),
				regexp.MustCompile(`^debug`),
				regexp.MustCompile(`^miner`),
			} {
				if r.MatchString(message.Method) {
					res := message.ErrorResponse(fmt.Errorf("the method %s does not exist/is not available", message.Method))
					res.Error.Code = json2.MethodNotFoundCode
					return res
				}
			}
			return nil
		},
	},
}

func validationErrorRes(msg *json2.JsonrpcMessage) *json2.JsonrpcMessage {
	for _, v := range requestValidations {
		if errMsg := v.fn(msg); errMsg != nil {
			return errMsg
		}
	}
	return nil
}

// getCacheDuration decides how long a response to a request should be cached for.
func getCacheDuration(request, response *json2.JsonrpcMessage) time.Duration {
	if response.IsError() {
		return defaultCacheExpirationLong
	}
	return defaultCacheExpiration
}

// asMsgValidatingWriting validates and reads the request into a *jsonrpcMessage and validates app-arbitrary conditions.
var errRequestNotPOST = errors.New("request method must be POST")
var errRequestNotContentTypeJSON = errors.New("request content type must be application/json (or left empty)")
var errRequestMissingBody = errors.New("request missing body")

// copyHeaders copies the headers from 'base' to the given response writer.
// Note it does NOT remove headers which do not exist on the src.
func copyHeaders(dst http.ResponseWriter, src http.Header) {
	for k := range src {
		dst.Header().Set(k, src.Get(k))
	}
}

// validateRequestWriting writes a jsonrpc message error to the responseWriter if the
// request is determined to be invalid.
// It reused the provided message's ID, if the message is not nil.
func validateRequestWriting(responseWriter http.ResponseWriter, request *http.Request, msg *json2.JsonrpcMessage) (ok bool) {
	if request.Method != "POST" {
		responseWriter.WriteHeader(http.StatusBadRequest)
		em := json2.ErrorMessage(fmt.Errorf("%w: you sent: %v", errRequestNotPOST, request.Method))
		em.Error.Code = json2.InvalidRequestCode
		if msg != nil {
			em = em.CopyWithID(msg.ID)
		}
		responseWriter.Write(em.MustJSONBytes())
		return false
	}
	if contentType := request.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") && contentType != "" {
		responseWriter.WriteHeader(http.StatusBadRequest)
		em := json2.ErrorMessage(fmt.Errorf("%w: you sent: %v", errRequestNotContentTypeJSON, contentType))
		em.Error.Code = json2.InvalidRequestCode
		if msg != nil {
			em = em.CopyWithID(msg.ID)
		}
		responseWriter.Write(em.MustJSONBytes())
		return false
	}
	return true
}

type cacheObject struct {
	header http.Header
	body   *json2.JsonrpcMessage
}

// handler2 is version 2 of the handler.
func handler2(responseWriter http.ResponseWriter, request *http.Request) {

	atomic.AddInt64(&stats.ReqCount, 1)

	if request.Body == nil {
		responseWriter.WriteHeader(http.StatusBadRequest)
		em := json2.ErrorMessage(errRequestMissingBody)
		em.Error.Code = json2.InvalidRequestCode
		responseWriter.Write(em.MustJSONBytes())
		return
	}

	// Read body as JSON, then parse to type.
	bodyJSON := json.RawMessage{}
	// I expect the Decoder to error if the body is not valid JSON.
	if err := json.NewDecoder(request.Body).Decode(&bodyJSON); err != nil {
		responseWriter.WriteHeader(http.StatusBadRequest)
		em := json2.ErrorMessage(err)
		em.Error.Code = json2.ParseErrorCode
		responseWriter.Write(em.MustJSONBytes())
		return
	}
	// I do not expect the Decoder to close after reading, but don't care if it actually has and errors.
	_ = request.Body.Close()

	// Parse.
	msgs, isBatch := json2.ParseMessage(bodyJSON)

	if isBatch {
		atomic.AddInt64(&stats.BatchReqCount, 1)
	}
	atomic.AddInt64(&stats.CallCount, int64(len(msgs)))

	if !validateRequestWriting(responseWriter, request, msgs[0]) {
		return
	}

	// Replies are the collection of JSONRPC messages in response
	// to the messages we've received and decoded.
	// If we're reading a batch request, we'll respond in kind, with a batch.
	// If it's just a single request (ie. of Object type),
	// we'll return the first item in the slice as a single response.
	replies := make([]*json2.JsonrpcMessage, len(msgs))

	// Loop over the request's messages and see if we
	// can fill any of them from the cache.
	// Messages are validated against JSON RPC schema type constraints
	// (eg. must not be a "notification"; must include an annotation 'method')
	// and blacklist-style constraints which fill responses with predefined
	// jsonrpc errors.
	for i, msg := range msgs {
		if msg == nil {
			continue
		}
		if errMsg := validationErrorRes(msg); errMsg != nil {
			replies[i] = errMsg
			continue
		}

		key, err := msg.CacheKey()
		if err != nil {
			responseWriter.WriteHeader(http.StatusInternalServerError)
			em := json2.ErrorMessage(err)
			em.Error.Code = json2.InternalErrorCode
			responseWriter.Write(em.MustJSONBytes())
			return
		}

		// Check cache.
		val, ok := c.Get(key)

		if ok {
			// Cache hit.
			atomic.AddInt64(&stats.CacheHitsCount, 1)

			cached := val.(*cacheObject)

			copyHeaders(responseWriter, cached.header)

			replies[i] = cached.body.CopyWithID(msg.ID)
			continue
		}
		// Cache miss.
		atomic.AddInt64(&stats.CacheMissesCount, 1)
	}

	// Assemble a batch of calls needed to forward to the origin.
	misses := []*json2.JsonrpcMessage{}
	for i, r := range replies {
		if msgs[i] == nil || r != nil {
			continue
		}
		misses = append(misses, msgs[i])
	}

	// Return early if the cache completely satisfied the request(s).
	if len(misses) == 0 {
		handlerWriteResponse(responseWriter, replies, isBatch)
		return
	}

	// JSON encode the new sub-batch for shipping to the origin.
	marshaled, err := json.Marshal(misses)
	if err != nil {
		responseWriter.WriteHeader(http.StatusInternalServerError)
		em := json2.ErrorMessage(err)
		em.Error.Code = json2.InternalErrorCode
		responseWriter.Write(em.MustJSONBytes())
		return
	}

	// Send batch request (which includes only the calls to which we don't have cached responses)
	// to origin.
	res, err := http.Post(remote.String(), "application/json", bytes.NewReader(marshaled))
	if err != nil {
		responseWriter.WriteHeader(http.StatusInternalServerError)
		em := json2.ErrorMessage(err)
		em.Error.Code = json2.InternalErrorCode
		responseWriter.Write(em.MustJSONBytes())
		return
	}

	// Read body as JSON, then parse to type.
	bodyJSON = json.RawMessage{}
	// I expect the Decoder to error if the body is not valid JSON.
	if err := json.NewDecoder(res.Body).Decode(&bodyJSON); err != nil {
		responseWriter.WriteHeader(http.StatusInternalServerError)
		em := json2.ErrorMessage(err)
		em.Error.Code = json2.InternalErrorCode
		responseWriter.Write(em.MustJSONBytes())
		return
	}
	// I do not expect the Decoder to close after reading, but don't care if it actually has and errors.
	_ = request.Body.Close()

	atomic.AddInt64(&stats.BytesReadFromUpstream, int64(len(bodyJSON)))

	// Parse.
	newReplies, _ := json2.ParseMessage(bodyJSON) // I assume we get a batch response to our batch request.
	nri := 0                                      // New Reply Index. We expect the order shipped to be preserved in the order received.
	var lowTTL time.Duration                      // Tracking the lowest TTL for the safest ultimate header Cache-Control value.
	for i, r := range replies {
		if msgs[i] == nil || r != nil {
			continue
		}

		// Look up our reply by index.
		newReply := newReplies[nri]
		nri++
		// Assign reply to existing replies filled from cache.
		// Note that the ID is good to go here.
		replies[i] = newReply

		// Handle the actual caching.
		key, err := msgs[i].CacheKey()
		if err != nil {
			responseWriter.WriteHeader(http.StatusInternalServerError)
			em := json2.ErrorMessage(err)
			em.Error.Code = json2.InternalErrorCode
			responseWriter.Write(em.MustJSONBytes())
			return
		}

		ttl := getCacheDuration(msgs[i], newReply)
		setHeaderCacheValue(res.Header, ttl) // For the cached response.

		// We may have different cache parameters for
		// responses to batched requests.
		// We can only return one header with one Cache-Control value.
		// So we choose the lowest cache value, if any,
		// as the value for our actual response.
		if ttl < lowTTL || lowTTL == 0 {
			// Augment the response header with the cache values.
			// The client should have a clue about how we're rolling.
			setHeaderCacheValue(responseWriter.Header(), ttl) // For the client response.
			lowTTL = ttl
		}

		co := &cacheObject{
			header: res.Header,
			body:   newReply,
		}

		// Cache the response.
		c.Set(key, co, ttl)
	}

	// Clone any and all headers from
	// the batch origin response.
	// Note that this could overwrite the Cache-Control, or Content-Type headers
	// that this application will set.
	copyHeaders(responseWriter, res.Header)

	handlerWriteResponse(responseWriter, replies, isBatch)
}

func setHeaderCacheValue(header http.Header, ttl time.Duration) {
	header.Set("Cache-Control", fmt.Sprintf("public, s-maxage=%.0f, max-age=%.0f",
		ttl.Truncate(time.Second).Seconds(), ttl.Truncate(time.Second).Seconds()))
}

func handlerWriteResponse(responseWriter http.ResponseWriter, responses []*json2.JsonrpcMessage, isBatch bool) {
	// Set the headers that are set for every response.
	responseWriter.Header().Set("Access-Control-Allow-Origin", "*")
	responseWriter.Header().Set("Content-Type", "application/json")

	var data []byte
	if isBatch {
		data, _ = json.Marshal(responses)
	} else {
		data, _ = json.Marshal(responses[0])
	}
	responseWriter.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	atomic.AddInt64(&stats.BytesSentToClients, int64(len(data)))
	responseWriter.Write(data)
}

var stats = Stats{InstanceStarted: time.Now()}

type Stats struct {
	InstanceStarted       time.Time
	ReqCount              int64
	BatchReqCount         int64
	CallCount             int64 // JSONRPC calls
	CacheHitsCount        int64
	CacheMissesCount      int64
	InvalidReqCount       int64
	BytesReadFromUpstream int64
	BytesSentToClients    int64
}

func (s *Stats) WriteToStream(w io.Writer) {
	// cachableCount is the sum of cache misses and hits.
	var cachableCount = stats.CacheHitsCount + stats.CacheMissesCount
	var cacheHitRatio float64
	if stats.ReqCount > 0 {
		cacheHitRatio = float64(s.CacheHitsCount) / float64(cachableCount)
	}
	var cacheMissRatio float64
	if stats.ReqCount > 0 {
		cacheMissRatio = float64(s.CacheMissesCount) / float64(cachableCount)
	}
	var batchRatio float64
	if stats.ReqCount > 0 {
		batchRatio = float64(s.BatchReqCount) / float64(stats.ReqCount)
	}

	fmt.Fprintf(w, "Uptime: %v\n", time.Since(stats.InstanceStarted).Truncate(time.Second))
	fmt.Fprintf(w, "Requests count: %d\n", stats.ReqCount)
	fmt.Fprintf(w, "Requests/sec: %.3f\n", float64(stats.ReqCount)/time.Since(stats.InstanceStarted).Seconds())
	fmt.Fprintf(w, "RPC Calls count: %d\n", s.CallCount)
	fmt.Fprintf(w, "RPC Batch count: %d\n", s.BatchReqCount)
	fmt.Fprintf(w, "RPC Batch magnitude average: %.3f\n", float64(s.CallCount-stats.ReqCount)/float64(s.BatchReqCount))
	fmt.Fprintf(w, "RPC Batch ratio: %.3f\n", batchRatio)
	fmt.Fprintf(w, "Cache hit ratio: %.3f\n", cacheHitRatio)
	fmt.Fprintf(w, "Cache hits: %d\n", s.CacheHitsCount)
	fmt.Fprintf(w, "Cache miss ratio: %.3f\n", cacheMissRatio)
	fmt.Fprintf(w, "Cache misses: %d\n", s.CacheMissesCount)
	fmt.Fprintf(w, "Read from upstream: %.3f MBytes\n", float64(s.BytesReadFromUpstream)/1000000)
	fmt.Fprintf(w, "Sent to clients: %.3f MBytes\n", float64(s.BytesSentToClients)/1000000)
	fmt.Fprintf(w, "Upstream traffic saved: %.3f MBytes\n", float64(s.BytesSentToClients-s.BytesReadFromUpstream)/1000000)
	fmt.Fprintf(w, "Upstream requests saved: %d\n", s.CacheHitsCount)
}

func aboutHandler(responseWriter http.ResponseWriter, request *http.Request) {
	responseWriter.Header().Set("Content-Type", "text/plain")
	responseWriter.WriteHeader(200)
	var w bytes.Buffer

	fmt.Fprintf(&w, "Hello from a go-ethereum JSONRPC caching proxy.\n\n")

	stats.WriteToStream(&w)

	responseWriter.Write(w.Bytes())
	return
}
