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

// proxyCacheResponse is the moneymaker.
// It is the only function that has access to both a request and associated response.
func proxyCacheResponse(request *jsonrpcMessage) func(*http.Response) error {
	return func(response *http.Response) error {
		key, err := request.cacheKey()
		if err != nil {
			return err
		}

		msg, err := responseToJSONRPC(response)
		if err != nil {
			return err
		}

		ttl := getCacheDuration(request, msg)

		// Augment the response header with the cache values.
		// The client should have a clue about how we're rolling.
		response.Header.Set("Cache-Control", fmt.Sprintf("public, s-maxage=%.0f, max-age=%.0f",
			ttl.Truncate(time.Second).Seconds(), ttl.Truncate(time.Second).Seconds()))

		// Cache the response.
		c.Set(key, response, ttl)
		c.Set(key+"msg", msg, ttl)

		return nil
	}
}

// asMsgValidatingWriting validates and reads the request into a *jsonrpcMessage and validates app-arbitrary conditions.
func asMsgValidatingWriting(responseWriter http.ResponseWriter, request *http.Request) *jsonrpcMessage {
	if request.Method != "POST" {
		responseWriter.WriteHeader(http.StatusBadRequest)
		responseWriter.Write([]byte("invalid method: method must be POST, you sent a " + request.Method))
		return nil
	}
	msg, e := requestToJSONRPC(request)
	if e != nil {
		responseWriter.WriteHeader(http.StatusBadRequest)
		responseWriter.Write([]byte(e.Error()))
		return nil
	}

	var err error
	for _, v := range requestValidations {
		if ec, errStr := v.fn(msg); ec != 0 {
			err = errors.New(errStr)
			responseWriter.WriteHeader(http.StatusBadRequest)
			errMsg := errorMessage(err)
			errMsg.Error.Code = ec
			responseWriter.Write(errMsg.mustJSONBytes())
			return nil
		}
	}

	return msg
}

// handler responds to requests.
func handler(responseWriter http.ResponseWriter, request *http.Request) {
	// start := time.Now()
	// defer func() {
	// log.Printf("Handler took: %v", time.Since(start))
	// }()

	msg := asMsgValidatingWriting(responseWriter, request)
	if msg == nil {
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
	proxy := httputil.NewSingleHostReverseProxy(remote)

	// We'll inspect and cache the response once it comes back.
	proxy.ModifyResponse = proxyCacheResponse(msg)

	// Set the origin as target.
	request.Host = remote.Host

	// Remove forwarded headers because this cache should be invisible.
	if request.Header.Get("X-Forwarded-For") != "" {
		request.Header.Del("X-Forwarded-For")
	}

	proxy.ServeHTTP(responseWriter, request)
}
