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

	"github.com/davecgh/go-spew/spew"
	json2 "helloworld/json"
)

func randomID() json.RawMessage {
	return json.RawMessage(fmt.Sprintf("%d", rand.Int()))
}

func mustStartTestOrigin(t *testing.T) *exec.Cmd {
	gethCmd := exec.Command("geth", "--dev", "--http", "--http.port", "8545")
	if err := gethCmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1 * time.Second) // Wait for geth to start up.

	r, err := url.Parse("http://localhost:8545")
	if err != nil {
		t.Fatal(err)
	}
	remote = r

	return gethCmd
}

func mustStartTestOriginB(b *testing.B) *exec.Cmd {
	gethCmd := exec.Command("geth", "--dev", "--http", "--http.port", "8545")
	if err := gethCmd.Start(); err != nil {
		b.Fatal(err)
	}

	r, err := url.Parse("http://localhost:8545")
	if err != nil {
		b.Fatal(err)
	}
	remote = r

	return gethCmd
}

func TestApp(t *testing.T) {
	g := mustStartTestOrigin(t)
	defer g.Process.Kill()

	type testcase struct {
		description string
		// reqBody contains a string representation of the request body.
		reqBody string
		// reqMod allows case modification of the otherwise default outgoing request.
		reqMod func(r *http.Request)
		// assertion defines what we want to show with the test.
		assertion func(t *testing.T, recorder *httptest.ResponseRecorder)
	}

	testcases := []testcase{
		{
			"OK request (batch includes null call and method-not-exist call)",
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
			"OK request (single, no params)",
			`{"jsonrpc":"2.0","id":__id,"method":"eth_blockNumber","params":[]}`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusOK {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
				}
			},
		},
		{
			"OK request (single with params)",
			`{"jsonrpc":"2.0","id":__id,"method":"eth_getBalance","params":["0xDf7D7e053933b5cC24372f878c90E62dADAD5d42", "latest"]}`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusOK {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
				}
			},
		},
		{
			"OK request but method does not exist",
			`{"jsonrpc":"2.0","id":__id,"method":"eth_noop","params":[]}`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusOK {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
				}
			},
		},
		{
			"Bad request: HTTP method not POST",
			`{"jsonrpc":"2.0","id":__id,"method":"eth_blockNumber","params":[]}`,
			func(r *http.Request) {
				r.Method = "GET"
			},
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusBadRequest {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusBadRequest)
				}
			},
		},
		{
			"Bad request: missing body",
			"",
			func(r *http.Request) {
				r.Body = nil
			},
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusBadRequest {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusBadRequest)
				}
			},
		},
		{
			"Bad request: not jsonrpc",
			`hello server`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusBadRequest {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusBadRequest)
				}
			},
		},
		{
			"Bad request: malformed jsonrpc",
			`{"jsonrpc":"2.0","id":__id,"method":"eth_blockNumber","params":[`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusBadRequest {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusBadRequest)
				}
			},
		},
		{
			"Bad request: malformed jsonrpc (2)",
			`{jsonrpc":"2.0","id":__id,"method":"eth_blockNumber","params":[]}`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusBadRequest {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusBadRequest)
				}
			},
		},
		{
			"Bad request: include 'result' annotation",
			`{"jsonrpc":"2.0","id":__id,"result":"0x0"}`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusOK {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
				}
			},
		},
		{
			"Bad request: missing 'id'",
			`{"jsonrpc":"2.0","method":"eth_getBalance","params":["0xDf7D7e053933b5cC24372f878c90E62dADAD5d42", "latest"]}`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusOK {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
				}
			},
		},
		{
			"Bad request: missing 'method'",
			`{"jsonrpc":"2.0", "id": __id, "params":[]}`,
			nil,
			func(t *testing.T, recorder *httptest.ResponseRecorder) {
				if status := recorder.Code; status != http.StatusOK {
					t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusOK)
				}
			},
		},
	}

	for i, c := range testcases {
		t.Logf("CASE %d: %s", i, c.description)

		c.reqBody = interpolateID(c.reqBody)

		reqs, reqIsBatch := json2.ParseMessage(json.RawMessage(c.reqBody))

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
		replies, replyIsBatch := json2.ParseMessage(raw)

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
						t.Errorf("mismatched ids: req: %s res: %s", reqMsg.ID, replies[j].ID)
					}
				} else if reqMsg == nil && replies[j] != nil {
					// The 'null' call case.
					t.Errorf("mismatched null call/response, req: %v, res: %v", reqMsg, replies[j])
				}
			}
		}

	}
}

func interpolateID(call string) string {
	for strings.Contains(call, "__id") {
		call = strings.Replace(call, "__id", string(randomID()), 1)
	}
	return call
}

func BenchmarkHandler2(b *testing.B) {
	b.ReportAllocs()

	g := mustStartTestOriginB(b)
	defer g.Process.Kill()

	d := interpolateID(`{"jsonrpc":"2.0","id":__id,"method":"eth_blockNumber","params":[]}`)
	data := bytes.NewBuffer([]byte(d))
	req, err := http.NewRequest("POST", "/", data)
	if err != nil {
		b.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	handler := http.HandlerFunc(handler2)
	b.ResetTimer()
	start := time.Now()
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	b.Logf("first request took: %v", time.Since(start))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rr = httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
	}
}

func TestSetHeaderCacheValue(t *testing.T) {
	h := http.Header{}
	setHeaderCacheValue(h, 1*time.Second)
	v := h.Get("Cache-Control")
	if v != "public, s-maxage=1, max-age=1" {
		t.Logf("%v", spew.Sdump(h))
		t.Fatal("unexpected Cache-Control header")
	}
}
