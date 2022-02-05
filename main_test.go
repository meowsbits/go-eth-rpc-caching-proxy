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
	"testing"
	"time"
)

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
				ID:      []byte(fmt.Sprintf("%d", rand.Int())),
				Version: "2.0",
				Method:  "eth_blockNumber",
				Params:  []byte(`[]`),
			},
			expectStatus: http.StatusOK,
		},
		{
			postData: jsonrpcMessage{
				ID:      []byte(fmt.Sprintf("%d", rand.Int())),
				Version: "2.0",
				Method:  "eth_getBalance",
				Params:  []byte(`["0xDf7D7e053933b5cC24372f878c90E62dADAD5d42", "latest"]`),
			},
			expectStatus: http.StatusOK,
		},
		{
			postData: jsonrpcMessage{
				ID:      []byte(fmt.Sprintf("%d", rand.Int())),
				Version: "2.0",
				Method:  "eth_blockNoop",
				Params:  []byte(`[]`),
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

		handler := http.HandlerFunc(handler)
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
			msg, err := responseToJSONRPC(rr.Result())
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

	handler := http.HandlerFunc(handler)
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

	handler := http.HandlerFunc(handler)
	handler.ServeHTTP(rr, req)

	dump, _ := httputil.DumpResponse(rr.Result(), true)
	t.Logf("<- %v", string(dump))

	if status := rr.Code; status != http.StatusOK {
		t.Errorf(
			"unexpected status: got (%v) want (%v)",
			status,
			http.StatusBadRequest,
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
		t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusBadRequest)
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
		t.Errorf("unexpected status: got (%v) want (%v)", status, http.StatusBadRequest)
	}
}
