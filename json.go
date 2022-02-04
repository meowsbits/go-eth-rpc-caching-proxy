package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	subscribeMethodSuffix   = "_subscribe"
	unsubscribeMethodSuffix = "_unsubscribe"
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
	// Validate request.
	if !msg.isCall() || msg.isNotification() || msg.isSubscribe() || msg.isUnsubscribe() {
		return "", errors.New("invalid request: must have 'method' field and not be subscription based")
	}
	out := msg.Method
	params := params{}
	err := json.Unmarshal(msg.Params, &params)
	if err != nil {
		return "", err
	}
	for _, p := range params {
		out += fmt.Sprintf("/%v", p)
	}
	return out, nil
}
