package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/XMZO/JustEnoughCerts/i18n"
)

// Error is the body of every error response. The client renders Code, a
// message key, in the viewer's language with Params; nothing in the
// response is text meant for people.
type Error struct {
	status int
	Code   i18n.Key       `json:"code" doc:"Message key, rendered by the client in the viewer's language"`
	Params map[string]any `json:"params,omitempty" doc:"Arguments of the message"`
	Fields []FieldError   `json:"fields,omitempty" doc:"The request fields at fault"`
}

// FieldError points at a request field at fault.
type FieldError struct {
	Location string   `json:"location" doc:"Where in the request, e.g. body.domains[1]"`
	Code     i18n.Key `json:"code" doc:"Message key"`
}

// NewError returns an error response with status, a message key and its
// arguments.
func NewError(status int, code i18n.Key, params map[string]any) *Error {
	return &Error{status: status, Code: code, Params: params}
}

// WithField adds a request field at fault, such as body.privateKey, with
// the message key that explains what is wrong with it.
func (e *Error) WithField(location string, code i18n.Key) *Error {
	e.Fields = append(e.Fields, FieldError{Location: location, Code: code})
	return e
}

func (e *Error) Error() string {
	if len(e.Params) == 0 {
		return string(e.Code)
	}
	return fmt.Sprintf("%s %v", e.Code, e.Params)
}

// GetStatus is the HTTP status of the response.
func (e *Error) GetStatus() int { return e.status }

// ContentType makes error responses plain JSON rather than problem+json.
func (e *Error) ContentType(string) string { return "application/json" }

// statusCodes are the messages for errors that huma or the router raise
// before a handler runs.
var statusCodes = map[int]i18n.Key{
	http.StatusBadRequest:            i18n.KernelErrorInvalidRequest,
	http.StatusUnauthorized:          i18n.KernelErrorUnauthorized,
	http.StatusNotFound:              i18n.KernelErrorNotFound,
	http.StatusMethodNotAllowed:      i18n.KernelErrorMethodNotAllowed,
	http.StatusRequestEntityTooLarge: i18n.KernelErrorTooLarge,
	http.StatusUnprocessableEntity:   i18n.KernelErrorInvalidRequest,
}

func codeFor(status int) i18n.Key {
	if code, ok := statusCodes[status]; ok {
		return code
	}
	if status >= http.StatusInternalServerError {
		return i18n.KernelErrorInternal
	}
	return i18n.KernelErrorInvalidRequest
}

// Every error huma builds, from request validation to a handler returning
// a plain error, goes through these hooks, so that all of them take the
// form of Error. The details of a server error go to the log only.
//
// Arrays are never null in the API: handlers return empty slices, and the
// schema says so, so that clients need no null checks.
func init() {
	huma.DefaultArrayNullable = false
	huma.NewErrorWithContext = func(ctx huma.Context, status int, msg string, errs ...error) huma.StatusError {
		e := &Error{status: status, Code: codeFor(status)}
		for _, err := range errs {
			var d huma.ErrorDetailer
			if errors.As(err, &d) && d.ErrorDetail().Location != "" {
				e.Fields = append(e.Fields, FieldError{Location: d.ErrorDetail().Location, Code: i18n.KernelErrorInvalidField})
			}
		}
		if status >= http.StatusInternalServerError {
			attrs := []any{"status", status, "msg", msg, "err", errors.Join(errs...)}
			if ctx != nil {
				attrs = append(attrs, "method", ctx.Method(), "path", ctx.URL().Path)
			}
			slog.Error("api request failed", attrs...)
		}
		return e
	}
	huma.NewError = func(status int, msg string, errs ...error) huma.StatusError {
		return huma.NewErrorWithContext(nil, status, msg, errs...)
	}
}

// writeError writes e outside of huma: for the router's own responses and
// for panics.
func writeError(w http.ResponseWriter, e *Error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(e.status)
	_ = json.NewEncoder(w).Encode(e)
}

// WriteError answers ctx with e, for middleware that stops a request.
func WriteError(ctx huma.Context, e *Error) {
	ctx.SetHeader("Content-Type", "application/json")
	ctx.SetStatus(e.status)
	_ = json.NewEncoder(ctx.BodyWriter()).Encode(e)
}

func statusHandler(status int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, NewError(status, codeFor(status), nil))
	}
}
