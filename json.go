package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	vsn = "2.0"

	subscribeMethodSuffix   = "_subscribe"
	unsubscribeMethodSuffix = "_unsubscribe"

	defaultErrorCode = -32000
)

var (
	null = json.RawMessage("null")
)

// A value of this type can a JSON-RPC request, notification, successful response or
// error response. Which one it is depends on the fields.
type jsonrpcMessage struct {
	Version string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Error   *jsonError      `json:"error,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
}

func (msg *jsonrpcMessage) isNotification() bool {
	return msg.ID == nil && msg.Method != ""
}

func (msg *jsonrpcMessage) isCall() bool {
	return msg.hasValidID() && msg.Method != ""
}

func (msg *jsonrpcMessage) isResponse() bool {
	return msg.hasValidID() && msg.Method == "" && msg.Params == nil && (msg.Result != nil || msg.Error != nil)
}

func (msg *jsonrpcMessage) hasValidID() bool {
	return len(msg.ID) > 0 && msg.ID[0] != '{' && msg.ID[0] != '['
}

func (msg *jsonrpcMessage) isSubscribe() bool {
	return strings.HasSuffix(msg.Method, subscribeMethodSuffix)
}

func (msg *jsonrpcMessage) isUnsubscribe() bool {
	return strings.HasSuffix(msg.Method, unsubscribeMethodSuffix)
}

func (msg *jsonrpcMessage) isError() bool {
	return msg.Error != nil
}

func (msg *jsonrpcMessage) mustJSONBytes() []byte {
	b, err := json.Marshal(msg)
	if err != nil {
		panic(err)
	}
	return b
}

func errorMessage(err error) *jsonrpcMessage {
	msg := &jsonrpcMessage{Version: vsn, ID: null, Error: &jsonError{
		Code:    defaultErrorCode,
		Message: err.Error(),
	}}
	return msg
}

func (msg *jsonrpcMessage) errorResponse(err error) *jsonrpcMessage {
	resp := errorMessage(err)
	resp.ID = msg.ID
	return resp
}

type jsonError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

func (err *jsonError) Error() string {
	if err.Message == "" {
		return fmt.Sprintf("json-rpc error %d", err.Code)
	}
	return err.Message
}

func (err *jsonError) ErrorCode() int {
	return err.Code
}

func (err *jsonError) ErrorData() interface{} {
	return err.Data
}

type params []interface{}

func (msg *jsonrpcMessage) cacheKey() (string, error) {
	out := msg.Method
	params := params{}
	err := json.Unmarshal(msg.Params, &params)
	if err != nil {
		return "", err
	}
	for _, p := range params {
		out += fmt.Sprintf("/%v", p)
	}
	return fmt.Sprintf("%x", sha1.Sum([]byte(out))), nil
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

	msg := &jsonrpcMessage{}
	err = json.Unmarshal(body, msg)
	if err != nil {
		return nil, err
	}
	return msg, nil
}
