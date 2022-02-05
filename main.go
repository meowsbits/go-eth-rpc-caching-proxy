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
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
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
	fn func(message *jsonrpcMessage) (int, string)
}

var requestValidations = []requestMsgValidation{
	{
		fn: func(message *jsonrpcMessage) (int, string) {
			if !message.isCall() {
				return defaultErrorCode, "request be valid JSON-RPC Call, must have 'method' annotation"
			}
			return 0, ""
		},
	},
	{
		fn: func(message *jsonrpcMessage) (int, string) {
			if message.isNotification() {
				return defaultErrorCode, "request must have 'id' annotation"
			}
			return 0, ""
		},
	},
	{
		fn: func(message *jsonrpcMessage) (int, string) {
			if message.isSubscribe() || message.isUnsubscribe() {
				return defaultErrorCode, "server does not support pubsub services"
			}
			return 0, ""
		},
	},
	{
		fn: func(message *jsonrpcMessage) (int, string) {
			if message.isSubscribe() || message.isUnsubscribe() {
				return defaultErrorCode, "server does not support pubsub services"
			}
			return 0, ""
		},
	},
	{
		fn: func(message *jsonrpcMessage) (int, string) {
			for _, r := range []*regexp.Regexp{
				regexp.MustCompile(`^admin`),
				regexp.MustCompile(`^personal`),
				regexp.MustCompile(`^debug`),
				regexp.MustCompile(`^miner`),
			} {
				if r.MatchString(message.Method) {
					return -32601, fmt.Sprintf("the method %s does not exist/is not available", message.Method)
				}
			}
			return 0, ""
		},
	},
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

	http.HandleFunc("/", handler)

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

// cacheMsgReqRes is the moneymaker.
// It is the only function that has access to both a request and associated response.
func cacheMsgReqRes(reqMsg *jsonrpcMessage) func(*http.Response) error {
	return func(response *http.Response) error {
		key, err := reqMsg.cacheKey()
		if err != nil {
			return err
		}

		resMsg, err := responseToJSONRPC(response)
		if err != nil {
			return err
		}

		ttl := getCacheDuration(reqMsg, resMsg)

		// Augment the response header with the cache values.
		// The client should have a clue about how we're rolling.
		response.Header.Set("Cache-Control", fmt.Sprintf("public, s-maxage=%.0f, max-age=%.0f",
			ttl.Truncate(time.Second).Seconds(), ttl.Truncate(time.Second).Seconds()))

		// Cache the response.
		c.Set(key, response, ttl)
		c.Set(key+"msg", resMsg, ttl)

		return nil
	}
}

// asMsgValidatingWriting validates and reads the request into a *jsonrpcMessage and validates app-arbitrary conditions.
var errRequestUnknownMsgType = errors.New("unable to decode to json rpc message")
var erRequestNotPOST = errors.New("request method must be POST")

func asMsgValidatingWriting(responseWriter http.ResponseWriter, request *http.Request) (*jsonrpcMessage, error) {
	if request.Method != "POST" {
		responseWriter.WriteHeader(http.StatusBadRequest)
		responseWriter.Write([]byte("invalid method: method must be POST, you sent a " + request.Method))
		return nil, erRequestNotPOST
	}

	// Since the request may be valid, but unknown encoding or data type (ie. batches),
	// we need to defer all unprocessable requests to the origin.
	msg, e := requestToJSONRPC(request)
	if e != nil {
		return nil, errRequestUnknownMsgType
	}

	// Now we know that the message is a jsonrpcMessage,
	// so we can validate it further.
	var err error
	for _, v := range requestValidations {
		if ec, errStr := v.fn(msg); ec != 0 {
			err = errors.New(errStr)
			responseWriter.WriteHeader(http.StatusBadRequest)
			errMsg := errorMessage(err)
			errMsg.Error.Code = ec
			responseWriter.Write(errMsg.mustJSONBytes())
			return nil, err
		}
	}

	return msg, nil
}

func handleProxy(responseWriter http.ResponseWriter, request *http.Request, msg *jsonrpcMessage) {
	// Handle the proxy.
	proxy := httputil.NewSingleHostReverseProxy(remote)

	// We'll inspect and cache the response once it comes back.
	if msg != nil {
		proxy.ModifyResponse = cacheMsgReqRes(msg)
	}

	// Set the origin as target.
	request.Host = remote.Host

	// Remove forwarded headers because this cache should be invisible.
	if request.Header.Get("X-Forwarded-For") != "" {
		request.Header.Del("X-Forwarded-For")
	}

	proxy.ServeHTTP(responseWriter, request)
}

// handler responds to requests.
func handler(responseWriter http.ResponseWriter, request *http.Request) {
	// start := time.Now()
	// defer func() {
	// log.Printf("Handler took: %v", time.Since(start))
	// }()

	msg, err := asMsgValidatingWriting(responseWriter, request)
	if errors.Is(err, errRequestUnknownMsgType) {
		// The request cannot be decoded as a simple jsonrpcMessage,
		// so we don't know how to handle it.
		handleProxy(responseWriter, request, msg)
		return
	} else if err != nil {
		return
	}

	key, err := msg.cacheKey()
	if err != nil {
		responseWriter.WriteHeader(http.StatusInternalServerError)
		errMsg := errorMessage(err)
		responseWriter.Write(errMsg.mustJSONBytes())
		return
	}

	// Run both gets, and confirm that they BOTH succeed.
	cachedResponseV, cacheHit := c.Get(key)
	cachedResponseMsgV, cacheHit2 := c.Get(key + "msg")
	if cacheHit && cacheHit2 {
		log.Printf("CACHE: hit / key=%v", key)

		cachedResponse := cachedResponseV.(*http.Response)
		cachedResponseMsg := cachedResponseMsgV.(*jsonrpcMessage)

		for k := range cachedResponse.Header {
			responseWriter.Header().Set(k, cachedResponse.Header.Get(k))
		}

		// Modify the cachedResponse value's 'id' field,
		// setting content length as required.
		m := &jsonrpcMessage{}
		*m = *cachedResponseMsg
		m.ID = msg.ID

		modBody := m.mustJSONBytes()

		responseWriter.Header().Set("Content-Length", fmt.Sprintf("%d", len(modBody)))

		if _, err := responseWriter.Write(modBody); err != nil {
			responseWriter.WriteHeader(http.StatusInternalServerError)
			errMsg := errorMessage(err)
			responseWriter.Write(errMsg.mustJSONBytes())
			return
		}
		return
	} else {
		log.Printf("CACHE: miss / key=%v", key)
	}

	// Handle the proxy.
	handleProxy(responseWriter, request, msg)
}
