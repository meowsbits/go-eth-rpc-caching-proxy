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
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func randomID() json.RawMessage {
	return json.RawMessage(fmt.Sprintf("%d", rand.Int()))
}

func mustStartTestOrigin(t *testing.T) *exec.Cmd {
	gethCmd := exec.Command("geth", "--dev", "--http", "--http.port", "8545")
	if err := gethCmd.Start(); err != nil {
		t.Fatal(err)
	}

	r, err := url.Parse("http://localhost:8545")
	if err != nil {
		t.Fatal(err)
	}
	remote = r

	return gethCmd
}

func TestPosts(t *testing.T) {
	g := mustStartTestOrigin(t)
	defer g.Process.Kill()

	type testCase struct {
		postData     jsonrpcMessage
		expectStatus int
	}
	cases := []testCase{
		{
			postData: jsonrpcMessage{
				ID:      randomID(),
				Version: "2.0",
				Method:  "eth_blockNumber",
				Params:  json.RawMessage(`[]`),
			},
			expectStatus: http.StatusOK,
		},
		{
			postData: jsonrpcMessage{
				ID:      randomID(),
				Version: "2.0",
				Method:  "eth_getBalance",
				Params:  json.RawMessage(`["0xDf7D7e053933b5cC24372f878c90E62dADAD5d42", "latest"]`),
			},
			expectStatus: http.StatusOK,
		},
		{
			postData: jsonrpcMessage{
				ID:      randomID(),
				Version: "2.0",
				Method:  "eth_blockNoop",
				Params:  json.RawMessage(`[]`),
			},
			expectStatus: http.StatusOK,
		},
	}

	runTest := func(i int, c testCase) {
		b, err := json.Marshal(c.postData)
		if err != nil {
			t.Fatal(i, err)
		}

		req, err := http.NewRequest("POST", "/", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")
		if err != nil {
			t.Fatal(i, err)
		}

		reqDump, err := httputil.DumpRequest(req, true)
		if err != nil {
			t.Fatal(i, err)
		}
		t.Logf("-> %v", string(reqDump))

		rr := httptest.NewRecorder()

		handler := http.HandlerFunc(handler2)
		handler.ServeHTTP(rr, req)

		dump, _ := httputil.DumpResponse(rr.Result(), true)
		t.Logf("<- %v", string(dump))

		if status := rr.Code; status != c.expectStatus {
			t.Errorf(
				"case: %d, unexpected status: got (%v) want (%v)",
				i,
				status,
				http.StatusOK,
			)
		} else {

			msg := &jsonrpcMessage{}
			err := json.NewDecoder(rr.Result().Body).Decode(msg)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(c.postData.ID, msg.ID) {
				t.Errorf("mismatched ids: request=%v response=%v", string(c.postData.ID), string(msg.ID))
			}
		}
	}

	for i, c := range cases {
		runTest(i, c)
		runTest(i, c)
		time.Sleep(defaultCacheExpiration)
		runTest(i, c)
		runTest(i, c)
	}
}

// TestHandlingMethodType expects that the server handles only POST methods.
func TestHandlingMethodType(t *testing.T) {

	req, err := http.NewRequest("GET", "/", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":5577006791947779410,"method":"eth_blockNumber","params":[]}`)))
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()

	handler := http.HandlerFunc(handler2)
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		dump, _ := httputil.DumpResponse(rr.Result(), true)
		t.Logf("<- %v", string(dump))
		t.Errorf(
			"unexpected status: got (%v) want (%v)",
			status,
			http.StatusBadRequest,
		)
	}
}

func TestBatchRequests(t *testing.T) {
	g := mustStartTestOrigin(t)
	defer g.Process.Kill()

	msgs := []jsonrpcMessage{
		{
			ID:      []byte(fmt.Sprintf("%d", rand.Int())),
			Version: "2.0",
			Method:  "eth_getBalance",
			Params:  []byte(`["0xDf7D7e053933b5cC24372f878c90E62dADAD5d42", "latest"]`),
		},
		{
			ID:      []byte(fmt.Sprintf("%d", rand.Int())),
			Version: "2.0",
			Method:  "eth_getBalance",
			Params:  []byte(`["0xab03e6e7a145f3c67a7eb4e3b40a5bb7d1dd484d", "latest"]`),
		},
	}

	b, err := json.Marshal(msgs)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest("POST", "/", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()

	handler := http.HandlerFunc(handler2)
	handler.ServeHTTP(rr, req)

	dump, _ := httputil.DumpResponse(rr.Result(), true)
	t.Logf("<- %v", string(dump))

	if status := rr.Code; status != http.StatusOK {
		t.Errorf(
			"unexpected status: got (%v) want (%v)",
			status,
			http.StatusOK,
		)
	}
}

// TestHandlingMethodType expects that the server handles only POST methods.
func TestHandler2(t *testing.T) {

	g := mustStartTestOrigin(t)
	defer g.Process.Kill()

	req, err := http.NewRequest("POST", "/", bytes.NewReader([]byte(`{"jsonrpc":"2.0","id":5577006791947779410,"method":"eth_blockNumber","params":[]}`)))
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	if err != nil {
		t.Fatal(err)
	}

	rr := httptest.NewRecorder()

	handler := http.HandlerFunc(handler2)
	handler.ServeHTTP(rr, req)
	req.Body.Close()

	reqDump, err := httputil.DumpRequest(req, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("-> %v", string(reqDump))

	dump, _ := httputil.DumpResponse(rr.Result(), true)
	t.Logf("<- %v", string(dump))
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
	}
}

// TestHandlingMethodType expects that the server handles only POST methods.
func TestHandler22(t *testing.T) {

	g := mustStartTestOrigin(t)
	defer g.Process.Kill()

	dataStr := `[
{"jsonrpc":"2.0","id":69,"method":"eth_blockNumber","params":[]},
{"jsonrpc":"2.0","id":42,"method":"eth_syncing","params":[]}]
`
	data := bytes.NewBuffer([]byte(dataStr))
	req, err := http.NewRequest("POST", "/", data)
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
	handler := http.HandlerFunc(handler2)
	handler.ServeHTTP(rr, req)

	dump, _ := httputil.DumpResponse(rr.Result(), true)
	t.Logf("<- %v", string(dump))
	if status := rr.Code; status != http.StatusOK {
		t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
	}
}

// TestHandlingMethodType expects that the server handles only POST methods.
func TestNullInBatchGethHandling(t *testing.T) {

	g := mustStartTestOrigin(t)
	defer g.Process.Kill()

	dataStr := `[
{"jsonrpc":"2.0","id":__id,"method":"eth_blockNumber","params":[]},
{"jsonrpc":"2.0","id":__id,"method":"eth_syncing","params":[]},
{"jsonrpc":"2.0","id":__id,"method":"web3_clientVersion","params":[]},
null,
{"jsonrpc":"2.0","id":__id,"method":"eth_noop","params":[]}
]
`

	run := func() {
		dataB := bytes.Replace([]byte(dataStr), []byte(`__id`), []byte(randomID()), -1)
		data := bytes.NewBuffer([]byte(dataB))
		req, err := http.NewRequest("POST", "/", data)
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
		handler := http.HandlerFunc(handler2)
		handler.ServeHTTP(rr, req)

		dump, _ := httputil.DumpResponse(rr.Result(), true)
		t.Logf("<- %v", string(dump))
		if status := rr.Code; status != http.StatusOK {
			t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
		}
	}
	for i := 0; i < 100; i++ {
		if i%10 == 0 {
			time.Sleep(500 * time.Millisecond)
		}
		run()
	}
	t.Logf("cache.hit=%d cache.miss=%d", mockCacheHit, mockCacheMiss)

}

func TestX(t *testing.T) {
	g := mustStartTestOrigin(t)
	defer g.Process.Kill()

	type testcase struct {
		// reqBody contains a string representation of the request body.
		reqBody string
		// reqMod allows case modification of the otherwise default outgoing request.
		reqMod func(r *http.Request)
		// assertion defines what we want to show with the test.
		assertion func(t *testing.T, recorder *httptest.ResponseRecorder)
	}

	testcases := []testcase{
		{
			`
		[
			{"jsonrpc":"2.0","id":__id,"method":"eth_blockNumber","params":[]},
			{"jsonrpc":"2.0","id":__id,"method":"eth_syncing","params":[]},
			{"jsonrpc":"2.0","id":__id,"method":"web3_clientVersion","params":[]},
			null,
			{"jsonrpc":"2.0","id":__id,"method":"eth_noop","params":[]},
			{"jsonrpc":"2.0","id":__id,"method":"web3_clientVersion","params":[]}
		]
		`, nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusOK {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
				}
			},
		},
		{
			`{"jsonrpc":"2.0","id":__id,"method":"eth_blockNumber","params":[]}`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusOK {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
				}
			},
		},
		{
			`{"jsonrpc":"2.0","id":__id,"method":"eth_getBalance","params":["0xDf7D7e053933b5cC24372f878c90E62dADAD5d42", "latest"]}`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusOK {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
				}
			},
		},
		// Bad request: missing 'id'
		{
			`{"jsonrpc":"2.0","method":"eth_getBalance","params":["0xDf7D7e053933b5cC24372f878c90E62dADAD5d42", "latest"]}`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusBadRequest {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusBadRequest)
				}
			},
		},
		// Bad request: missing 'method'
		{
			`{"jsonrpc":"2.0", "id": __id, "params":[]}`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusBadRequest {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusBadRequest)
				}
			},
		},
		// Bad request: not jsonrpc
		{
			`hello server`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusBadRequest {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusBadRequest)
				}
			},
		},
		// Bad request: WTF
		{
			`hello server`,
			func(r *http.Request) {
				r.Method = "GET"
			},
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusBadRequest {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusBadRequest)
				}
			},
		},
	}

	for i, c := range testcases {
		t.Log()
		t.Logf("CASE %d", i)

		for strings.Contains(c.reqBody, "__id") {
			c.reqBody = strings.Replace(c.reqBody, "__id", string(randomID()), 1)
		}

		reqs, reqIsBatch := parseMessage(json.RawMessage(c.reqBody))

		data := bytes.NewBuffer([]byte(c.reqBody))
		req, err := http.NewRequest("POST", "/", data)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json; charset=UTF-8")

		if c.reqMod != nil {
			c.reqMod(req)
		}

		reqDump, err := httputil.DumpRequest(req, true)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("-> %v", string(reqDump))

		rr := httptest.NewRecorder()
		handler := http.HandlerFunc(handler2)
		handler.ServeHTTP(rr, req)

		dump, _ := httputil.DumpResponse(rr.Result(), true)
		t.Logf("<- %v", string(dump))

		if c.assertion != nil {
			c.assertion(t, rr)
		}

		raw := json.RawMessage{}
		err = json.NewDecoder(rr.Body).Decode(&raw)
		if err != nil {
			t.Error(err)
		}
		replies, replyIsBatch := parseMessage(raw)

		if len(replies) == 0 {
			t.Fatal("want > 1 replies, got", len(replies))
		}

		if reqIsBatch != replyIsBatch {
			t.Fatalf("mismatch batch types: req: %v res: %v", reqIsBatch, replyIsBatch)
		}

		// Not all requests will have > 0 msgs.
		// Some requsts will use intentionally malformed bodies.
		if len(reqs) > 0 {
			for j, reqMsg := range reqs {
				if reqMsg != nil && reqMsg.ID != nil {
					if string(replies[j].ID) != string(reqMsg.ID) {
						t.Errorf("mismatched ids: req: %v res: %v", reqMsg.ID, replies[j].ID)
					}
				} else if reqMsg == nil && replies[j] != nil {
					// The 'null' call case.
					t.Errorf("mismatched null call/response, req: %v, res: %v", reqMsg, replies[j])
				}
			}
		}

	}
}
