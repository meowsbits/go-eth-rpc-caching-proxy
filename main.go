// Copyright 2019 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// [START gae_go111_app]

// Sample helloworld is an App Engine app.
package main

// [START import]
import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"time"

	"github.com/patrickmn/go-cache"
)

// [END import]
// [START main_func]

const defaultCacheExpiration = 1 * time.Second
const defaultCacheExpirationLong = 60 * time.Second

// Create a cache with a default expiration time of 5 minutes, and which
// purges expired items every 10 minutes
var c = cache.New(defaultCacheExpiration, 1*time.Second)

var remote *url.URL

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

// [END main_func]

func proxyCacheResponse(request *jsonrpcMessage) func(*http.Response) error {
	return func(response *http.Response) error {
		key, err := request.cacheKey()
		if err != nil {
			return err
		}

		ttl := defaultCacheExpiration

		msg, err := responseToJSONRPC(response)
		if err != nil {
			return err
		}
		if msg.Error != nil {
			ttl = defaultCacheExpirationLong
		}

		// Cache the response.
		c.Set(key, response, ttl)

		// But the Body ReadCloser doesn't get stored.
		// We need to cache the body as text.
		body, err := io.ReadAll(response.Body)
		if err != nil {
			return err
		}
		if err := response.Body.Close(); err != nil {
			return err
		}
		response.Body = io.NopCloser(bytes.NewReader(body))
		response.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
		response.ContentLength = int64(len(body))

		c.Set(key+"body", body, ttl)

		response.Header.Set("Cache-Control", "public, s-maxage=1, max-age=1")
		return nil
	}
}

func responseToJSONRPC(response *http.Response) (*jsonrpcMessage, error) {
	// Capture the body so we can safely read it,
	// and then reinstall it.
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	if err := response.Body.Close(); err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	response.ContentLength = int64(len(body))

	msg := &jsonrpcMessage{}
	err = json.Unmarshal(body, msg)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func requestToJSONRPC(request *http.Request) (*jsonrpcMessage, error) {
	// Capture the body so we can safely read it,
	// and then reinstall it.
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	if err := request.Body.Close(); err != nil {
		return nil, err
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	request.Header.Set("Content-Length", fmt.Sprintf("%d", len(body)))
	request.ContentLength = int64(len(body))

	msg := &jsonrpcMessage{}
	err = json.Unmarshal(body, msg)
	if err != nil {
		return nil, err
	}
	return msg, nil
}

func replaceResponseID(originalMsg *jsonrpcMessage, response *http.Response) error {
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if err := response.Body.Close(); err != nil {
		return err
	}

	bodyMsg := &jsonrpcMessage{}
	err = json.Unmarshal(body, bodyMsg)
	if err != nil {
		return err
	}
	// Match up the original request and the cached response call IDs.
	bodyMsg.ID = originalMsg.ID

	responseBody, err := json.Marshal(bodyMsg)
	if err != nil {
		return err
	}

	response.Body = io.NopCloser(bytes.NewReader(responseBody))
	response.Header.Set("Content-Length", fmt.Sprintf("%d", len(responseBody)))
	response.ContentLength = int64(len(responseBody))

	return nil
}

// replaceID uses the id from bodyA in bodyB
func replaceID(msg *jsonrpcMessage, body []byte) (modifiedBody []byte, err error) {
	bodyMsgB := &jsonrpcMessage{}
	err = json.Unmarshal(body, bodyMsgB)
	if err != nil {
		return nil, err
	}

	// Match up the original request and the cached response call IDs.
	bodyMsgB.ID = msg.ID

	modifiedBody, err = json.Marshal(bodyMsgB)
	if err != nil {
		return nil, err
	}
	return modifiedBody, nil
}

// [START handler]

// handler responds to requests.
func handler(responseWriter http.ResponseWriter, request *http.Request) {
	// start := time.Now()
	// defer func() {
	// log.Printf("Handler took: %v", time.Since(start))
	// }()

	if request.Method != "POST" {
		responseWriter.Write([]byte("invalid method: method must be POST"))
		responseWriter.WriteHeader(400)
		return
	}

	msg, err := requestToJSONRPC(request)
	if err != nil {
		log.Printf("invalid request: error: %v", err)
		responseWriter.Write([]byte(err.Error()))
		responseWriter.WriteHeader(400)
		return
	}

	key, err := msg.cacheKey()
	if err != nil {
		responseWriter.WriteHeader(400)
		responseWriter.Write([]byte(err.Error()))
		return
	}

	v, cacheHit := c.Get(key)
	if cacheHit {
		log.Printf("CACHE: hit / key=%v", key)

		cachedResponse := v.(*http.Response)
		cachedResponseBody, ok := c.Get(key + "body")
		if !ok {
			// Handle hopefully-impossible case where the cache does not have the info.
			responseWriter.WriteHeader(500)
			responseWriter.Write([]byte("missing cached body for request"))
			return
		}
		cachedResponseBodyBytes := cachedResponseBody.([]byte)

		for k := range cachedResponse.Header {
			responseWriter.Header().Set(k, cachedResponse.Header.Get(k))
		}

		// Modify the cachedResponse value's 'id' field,
		// setting content length as required.
		modBody, err := replaceID(msg, cachedResponseBodyBytes)
		if err != nil {
			responseWriter.WriteHeader(500)
			responseWriter.Write([]byte(err.Error()))
		}

		if _, err := responseWriter.Write(modBody); err != nil {
			responseWriter.WriteHeader(500)
			responseWriter.Write([]byte(err.Error()))
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

// [END handler]
// [END gae_go111_app]
