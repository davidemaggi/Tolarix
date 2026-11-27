package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

// maxBody limits JSON request bodies.
const maxBody = 1 << 20

// apiError is an error with an HTTP status, a stable code and a message for
// the user. Handlers return it from helpers; writeErr renders it.
type apiError struct {
	Status  int
	Code    string
	Message string
	Fields  map[string]string
}

func (e *apiError) Error() string { return e.Message }

func errBadRequest(code, format string, a ...any) *apiError {
	return &apiError{Status: http.StatusBadRequest, Code: code, Message: fmt.Sprintf(format, a...)}
}

func errValidation(fields map[string]string) *apiError {
	return &apiError{Status: http.StatusBadRequest, Code: "validation", Message: "Some fields need attention.", Fields: fields}
}

func errNotFound(what string) *apiError {
	return &apiError{Status: http.StatusNotFound, Code: "not_found", Message: "This " + what + " does not exist (it may have been deleted)."}
}

func errConflict(code, msg string) *apiError {
	return &apiError{Status: http.StatusConflict, Code: code, Message: msg}
}

// writeErr renders err. Unknown errors become a generic 500 and are logged
// by the caller's logger, never shown (they may contain internals).
func (s *server) writeErr(w http.ResponseWriter, r *http.Request, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		var b struct {
			Error struct {
				Code    string            `json:"code"`
				Message string            `json:"message"`
				Fields  map[string]string `json:"fields,omitempty"`
			} `json:"error"`
		}
		b.Error.Code, b.Error.Message, b.Error.Fields = ae.Code, ae.Message, ae.Fields
		writeJSON(w, ae.Status, b)
		return
	}
	s.log.Error("API request failed", "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal", "Something went wrong. Check the server logs.")
}

// decodeJSON reads a JSON body into v, rejecting unknown fields, trailing
// data and non-JSON content types (a cross-site form cannot send JSON
// without a CORS preflight, which this server never allows).
func decodeJSON(r *http.Request, v any) error {
	if ct := r.Header.Get("Content-Type"); ct != "" {
		mt, _, err := mime.ParseMediaType(ct)
		if err != nil || mt != "application/json" {
			return &apiError{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "Send the request body as application/json."}
		}
	} else {
		return &apiError{Status: http.StatusUnsupportedMediaType, Code: "unsupported_media_type", Message: "Send the request body as application/json."}
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, maxBody+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		msg := err.Error()
		if errors.Is(err, io.EOF) {
			msg = "the request body is empty"
		}
		return errBadRequest("invalid_json", "The request is not valid: %s.", strings.TrimPrefix(msg, "json: "))
	}
	if dec.More() {
		return errBadRequest("invalid_json", "The request has extra data after the JSON body.")
	}
	return nil
}
