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

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"testing"
	"time"
)

func TestIndexHandler(t *testing.T) {
	runTest := func() {
		d := jsonrpcMessage{
			ID:      []byte(fmt.Sprintf("%d", rand.Int())),
			Version: "2.0",
			Method:  "eth_blockNumber",
			Params:  []byte(`[]`),
		}

		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}

		req, err := http.NewRequest("POST", "/", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
		if err != nil {
			t.Fatal(err)
		}

		reqDump, err := httputil.DumpRequest(req, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("-> %v", string(reqDump))

		rr := httptest.NewRecorder()

		handler := http.HandlerFunc(handler)
		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			dump, _ := httputil.DumpResponse(rr.Result(), true)
			t.Logf("response: %v", string(dump))
			t.Errorf(
				"unexpected status: got (%v) want (%v)",
				status,
				http.StatusOK,
			)
		} else {
			msg, err := responseToJSONRPC(rr.Result())
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(d.ID, msg.ID) {
				t.Errorf("mismatched ids: request=%v response=%v", string(d.ID), string(msg.ID))
			}

			dump, err := httputil.DumpResponse(rr.Result(), true)
			if err != nil {
				t.Errorf("dump error: %v", err)
			}
			t.Logf("<- %s", string(dump))

		}
	}

	runTest()
	runTest()
	time.Sleep(1 * time.Second)
	runTest()
	runTest()
}

func TestIndexHandler2(t *testing.T) {
	runTest := func() {
		d := jsonrpcMessage{
			ID:      []byte(fmt.Sprintf("%d", rand.Int())),
			Version: "2.0",
			Method:  "eth_getBalance",
			Params:  []byte(`["0xDf7D7e053933b5cC24372f878c90E62dADAD5d42", "latest"]`),
		}

		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}

		req, err := http.NewRequest("POST", "/", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
		if err != nil {
			t.Fatal(err)
		}

		reqDump, err := httputil.DumpRequest(req, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("-> %v", string(reqDump))

		rr := httptest.NewRecorder()

		handler := http.HandlerFunc(handler)
		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			dump, _ := httputil.DumpResponse(rr.Result(), true)
			t.Logf("response: %v", string(dump))
			t.Errorf(
				"unexpected status: got (%v) want (%v)",
				status,
				http.StatusOK,
			)
		} else {
			msg, err := responseToJSONRPC(rr.Result())
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(d.ID, msg.ID) {
				t.Errorf("mismatched ids: request=%v response=%v", string(d.ID), string(msg.ID))
			}

			dump, err := httputil.DumpResponse(rr.Result(), true)
			if err != nil {
				t.Errorf("dump error: %v", err)
			}
			t.Logf("<- %s", string(dump))

		}
	}

	runTest()
	runTest()
	time.Sleep(1 * time.Second)
	runTest()
	runTest()
}

func TestIndexHandlerError(t *testing.T) {
	runTest := func() {
		d := jsonrpcMessage{
			ID:      []byte(fmt.Sprintf("%d", rand.Int())),
			Version: "2.0",
			Method:  "eth_blockNoop",
			Params:  []byte(`[]`),
		}

		b, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}

		req, err := http.NewRequest("POST", "/", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
		if err != nil {
			t.Fatal(err)
		}

		reqDump, err := httputil.DumpRequest(req, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("-> %v", string(reqDump))

		rr := httptest.NewRecorder()

		handler := http.HandlerFunc(handler)
		handler.ServeHTTP(rr, req)

		if status := rr.Code; status != http.StatusOK {
			dump, _ := httputil.DumpResponse(rr.Result(), true)
			t.Logf("response: %v", string(dump))
			t.Errorf(
				"unexpected status: got (%v) want (%v)",
				status,
				http.StatusOK,
			)
		} else {
			msg, err := responseToJSONRPC(rr.Result())
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(d.ID, msg.ID) {
				t.Errorf("mismatched ids: request=%v response=%v", string(d.ID), string(msg.ID))
			}

			dump, err := httputil.DumpResponse(rr.Result(), true)
			if err != nil {
				t.Errorf("dump error: %v", err)
			}
			t.Logf("<- %s", string(dump))

		}
	}

	runTest()
	runTest()
	time.Sleep(1 * time.Second)
	runTest()
	runTest()
}
