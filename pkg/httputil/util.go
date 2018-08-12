package httputil

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type ApiError struct {
	StatusCode int    `json:"statusCode"`
	Message    string `json:"message"`
	Reason     string `json:"reason"`
	headers    map[string]string
}

func (e *ApiError) AddHeader(key, value string) {
	if e.headers == nil {
		e.headers = make(map[string]string)
	}
	e.headers[key] = value
}

func (e *ApiError) MessageF(msg string, a ...interface{}) *ApiError {
	e.Message = fmt.Sprintf(msg, a...)
	return e
}

func (e *ApiError) Cause(cause string) *ApiError {
	e.Reason = cause
	return e
}

func (e *ApiError) Write(w http.ResponseWriter) error {
	w.Header().Add("Content-Type", "application/json")
	for key, value := range e.headers {
		w.Header().Add(key, value)
	}
	w.WriteHeader(e.StatusCode)
	return json.NewEncoder(w).Encode(e)
}

func HttpError(statusCode int, reason string) *ApiError {
	if reason == "" {
		reason = "Unknown"
	}
	return &ApiError{Reason: reason, StatusCode: statusCode}
}
