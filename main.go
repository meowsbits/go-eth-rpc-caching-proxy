// package main implements a caching proxy for an ethereum json rpc origin.
//
// VALIDATION

// The server validates that the request is of POST method type.
// Only simple jsonrpcMessage data-typed requests are cached (batches are not supported).
// In the case of batches or other unsupported (not decodable-as jsonrpcMessage), the requests
// are proxied to the origin as-is.
// If the request is decodable as a simple jsonnrpcMessage type, the request
// is further validated according to the validations described in requestValidations, below.
//
// CACHING
//
// Cacheable requests are keyed on their method and params, concatenated.
// The actual key is a sha1 sum of this concatenation.
//

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/patrickmn/go-cache"
)

const defaultCacheExpiration = 1 * time.Second
const defaultCacheExpirationLong = 60 * time.Second

// Create a cache with a default expiration time of 5 minutes, and which
// purges expired items every 10 minutes
var c = cache.New(defaultCacheExpiration, 1*time.Second)

// remote is the parsed form of the global app setting of the proxy origin.
var remote *url.URL

type requestMsgValidation struct {
	fn func(message *jsonrpcMessage) *jsonrpcMessage
}

var requestValidations = []requestMsgValidation{
	{
		fn: func(message *jsonrpcMessage) *jsonrpcMessage {
			if !message.isCall() {
				return message.errorResponse(errors.New("request be valid JSON-RPC Call, must have 'method' annotation"))
			}
			return nil
		},
	},
	{
		fn: func(message *jsonrpcMessage) *jsonrpcMessage {
			if message.isNotification() {
				return message.errorResponse(errors.New("request must have 'id' annotation"))
			}
			return nil
		},
	},
	{
		fn: func(message *jsonrpcMessage) *jsonrpcMessage {
			if message.isSubscribe() || message.isUnsubscribe() {
				return message.errorResponse(errors.New("server does not support pubsub services"))
			}
			return nil
		},
	},
	{
		fn: func(message *jsonrpcMessage) *jsonrpcMessage {
			if message.isSubscribe() || message.isUnsubscribe() {
				return message.errorResponse(errors.New("server does not support pubsub services"))
			}
			return nil
		},
	},
	{
		fn: func(message *jsonrpcMessage) *jsonrpcMessage {
			for _, r := range []*regexp.Regexp{
				regexp.MustCompile(`^admin`),
				regexp.MustCompile(`^personal`),
				regexp.MustCompile(`^debug`),
				regexp.MustCompile(`^miner`),
			} {
				if r.MatchString(message.Method) {
					res := message.errorResponse(fmt.Errorf("the method %s does not exist/is not available", message.Method))
					res.Error.Code = -32601
					return res
				}
			}
			return nil
		},
	},
}

func validationErrorRes(msg *jsonrpcMessage) *jsonrpcMessage {
	for _, v := range requestValidations {
		if errMsg := v.fn(msg); errMsg != nil {
			return errMsg
		}
	}
	return nil
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

// getCacheDuration decides how long a response to a request should be cached for.
func getCacheDuration(request, response *jsonrpcMessage) time.Duration {
	if response.isError() {
		return defaultCacheExpirationLong
	}
	return defaultCacheExpiration
}

// asMsgValidatingWriting validates and reads the request into a *jsonrpcMessage and validates app-arbitrary conditions.
var errRequestNotPOST = errors.New("request method must be POST")

// cloneHeaders copies the headers from 'base' to the given response writer.
func cloneHeaders(base *http.Response, w http.ResponseWriter) {
	for k := range base.Header {
		w.Header().Set(k, base.Header.Get(k))
	}
}

func validateRequestWriting(responseWriter http.ResponseWriter, request *http.Request) (ok bool) {
	if request.Method != "POST" {
		responseWriter.WriteHeader(http.StatusBadRequest)
		responseWriter.Write([]byte(errRequestNotPOST.Error() + ", you sent: " + request.Method))
		return false
	}
	if contentType := request.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") && contentType != "" {
		responseWriter.WriteHeader(http.StatusBadRequest)
		responseWriter.Write([]byte(errRequestNotPOST.Error() + ", you sent: '" + contentType + "'"))
		return false
	}
	return true
}

// handler2 is version 2 of the handler.
func handler2(responseWriter http.ResponseWriter, request *http.Request) {
	if !validateRequestWriting(responseWriter, request) {
		return
	}

	// Read body as JSON, then parse to type.
	bodyJSON := json.RawMessage{}
	// I expect the Decoder to error if the body is not valid JSON.
	if err := json.NewDecoder(request.Body).Decode(&bodyJSON); err != nil {
		responseWriter.WriteHeader(http.StatusBadRequest)
		responseWriter.Write([]byte(err.Error()))
		return
	}
	// I do not expect the Decoder to close after reading, but don't care if it actually has and errors.
	_ = request.Body.Close()
	// Parse.
	msgs, isBatch := parseMessage(bodyJSON)

	replies := make([]*jsonrpcMessage, len(msgs))

	for i, msg := range msgs {
		// TODO: improve sanitation and validation before handling too seriously.
		if msg == nil {
			continue
		}
		if errMsg := validationErrorRes(msg); errMsg != nil {
			replies[i] = errMsg
			continue
		}

		key, err := msg.cacheKey()
		if err != nil {
			responseWriter.WriteHeader(http.StatusInternalServerError)
			responseWriter.Write([]byte(err.Error()))
			return
		}

		// Check cache.
		cachedResponseV, cacheHit := c.Get(key)
		cachedResponseMsgV, cacheHit2 := c.Get(key + "msg")

		//   Cache hit.
		if cacheHit && cacheHit2 {
			// log.Printf("CACHE: hit / key=%v", key)

			// To typed vars.
			cachedResponse := cachedResponseV.(*http.Response)
			cachedResponseMsg := cachedResponseMsgV.(*jsonrpcMessage)

			cloneHeaders(cachedResponse, responseWriter)

			replies[i] = cachedResponseMsg.copyWithID(msg.ID)
			continue
		}

		//   Cache miss.
		// log.Printf("CACHE: miss / key=%v", key)
	}

	// Assemble a batch of calls needed to forward to the origin.
	misses := []*jsonrpcMessage{}
	for i, r := range replies {
		if msgs[i] == nil || r != nil {
			continue
		}
		misses = append(misses, msgs[i])
	}

	// Return early if the cache completely satisfied request.
	if len(misses) == 0 {
		responseWriter.Header().Set("Content-Type", "application/json")
		var data []byte
		if isBatch {
			data, _ = json.Marshal(replies)
		} else {
			data, _ = json.Marshal(replies[0])
		}
		responseWriter.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
		responseWriter.Write(data)
		return
	}

	// JSON encode the new sub-batch for shipping to the origin.
	marshaled, err := json.Marshal(misses)
	if err != nil {
		responseWriter.WriteHeader(http.StatusInternalServerError)
		responseWriter.Write([]byte(err.Error()))
		return
	}

	// Send batch request (which includes only the calls to which we don't have cached responses)
	// to origin.
	res, err := http.Post(remote.String(), "application/json", bytes.NewReader(marshaled))
	if err != nil {
		responseWriter.WriteHeader(http.StatusInternalServerError)
		responseWriter.Write([]byte(err.Error()))
		return
	}

	// Read body as JSON, then parse to type.
	bodyJSON = json.RawMessage{}
	// I expect the Decoder to error if the body is not valid JSON.
	if err := json.NewDecoder(res.Body).Decode(&bodyJSON); err != nil {
		responseWriter.WriteHeader(http.StatusBadRequest)
		responseWriter.Write([]byte(err.Error()))
		return
	}
	// I do not expect the Decoder to close after reading, but don't care if it actually has and errors.
	_ = request.Body.Close()
	// Parse.
	newReplies, _ := parseMessage(bodyJSON) // I assume we get a batch response to our batch request.
	nri := 0                                // New Reply Index. We expect the order shipped to be preserved in the order received.
	var lowTTL time.Duration                // Tracking the lowest TTL for the safest ultimate header Cache-Control value.
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

		// [Maybe Prettier]
		// cacheMsgReqRes(msgs[i])(res)

		// [Ugly] Manually handle the caching because wrapping the cache in
		// a function which needs a request
		key, err := msgs[i].cacheKey()
		if err != nil {
			responseWriter.WriteHeader(http.StatusBadRequest)
			responseWriter.Write([]byte(err.Error()))
			return
		}

		ttl := getCacheDuration(msgs[i], newReply)

		if ttl < lowTTL || lowTTL == 0 {
			// Augment the response header with the cache values.
			// The client should have a clue about how we're rolling.
			responseWriter.Header().Set("Cache-Control", fmt.Sprintf("public, s-maxage=%.0f, max-age=%.0f",
				ttl.Truncate(time.Second).Seconds(), ttl.Truncate(time.Second).Seconds()))
			lowTTL = ttl
		}

		// Cache the response.
		c.Set(key, res, ttl)
		c.Set(key+"msg", newReply, ttl)
	}

	// Clone any and all headers from
	// the batch origin response.
	// Note that this could overwrite the Cache-Control, or Content-Type headers
	// that this application will set.
	cloneHeaders(res, responseWriter)

	responseWriter.Header().Set("Content-Type", "application/json")
	var data []byte
	if isBatch {
		data, _ = json.Marshal(replies)
	} else {
		data, _ = json.Marshal(replies[0])
	}
	responseWriter.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	responseWriter.Write(data)
	
}
