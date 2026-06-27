package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func Test_requestIDMiddlewareUsesInboundRequestID(t *testing.T) {
	const expectedRequestID = "incoming-request-id"

	var seenRequestID string
	handler := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRequestID = w.Header().Get(requestIDHeader)
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest("GET", "http://lin.ko/api/stats", nil)
	req.Header.Set(requestIDHeader, expectedRequestID)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if seenRequestID != expectedRequestID {
		t.Fatalf("unexpected request ID seen by handler: got %q, want %q", seenRequestID, expectedRequestID)
	}
	if got := rr.Header().Get(requestIDHeader); got != expectedRequestID {
		t.Fatalf("unexpected response request ID: got %q, want %q", got, expectedRequestID)
	}
}

func Test_requestIDMiddlewareGeneratesRequestID(t *testing.T) {
	var seenRequestID string
	handler := requestIDMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenRequestID = w.Header().Get(requestIDHeader)
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest("GET", "http://lin.ko/api/stats", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if seenRequestID == "" {
		t.Fatal("expected request ID to be set before handler execution")
	}
	if got := rr.Header().Get(requestIDHeader); got == "" {
		t.Fatal("expected request ID response header to be set")
	} else if got != seenRequestID {
		t.Fatalf("unexpected response request ID: got %q, want %q", got, seenRequestID)
	}
}

func Test_requestLogger(t *testing.T) {
	logBuffer := &bytes.Buffer{}

	logger := slog.New(slog.NewTextHandler(logBuffer, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				return slog.Time(slog.TimeKey, time.Date(2023, 10, 1, 12, 34, 57, 0, time.UTC))
			case "duration":
				return slog.Duration("duration", 42*time.Millisecond)
			default:
				return a
			}
		},
	}))

	requestLoggerMiddleware := requestLogger(logger)
	dummyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("unexpected request body read error: %v", err)
		}
		if string(body) != "payload" {
			t.Fatalf("unexpected request body: got %q, want %q", body, "payload")
		}
		w.WriteHeader(http.StatusCreated)
		if _, err := w.Write([]byte("ok")); err != nil {
			t.Fatalf("unexpected response write error: %v", err)
		}
	})
	loggedHandler := requestIDMiddleware(requestLoggerMiddleware(dummyHandler))

	req := httptest.NewRequest("POST", "http://lin.ko/api/stats", bytes.NewBufferString("payload"))
	req.Header.Set(requestIDHeader, "request-123")
	req.RemoteAddr = "192.0.2.1:1234"
	rr := httptest.NewRecorder()
	loggedHandler.ServeHTTP(rr, req)

	const expectedLogString = `time=2023-10-01T12:34:57.000Z level=INFO msg="Served request" method=POST path=/api/stats request_id=request-123 client_ip=192.0.2.1:1234 duration=42ms request_body_bytes=7 response_status=201 response_body_bytes=2` + "\n"
	const expectedStatusCode = http.StatusCreated

	if got := logBuffer.String(); got != expectedLogString {
		t.Errorf("unexpected log output:\n got: %q\n want: %q", got, expectedLogString)
	}
	if rr.Code != expectedStatusCode {
		t.Errorf("unexpected status code: got %d, want %d", rr.Code, expectedStatusCode)
	}
}

func Test_requestLoggerAddsUser(t *testing.T) {
	logBuffer := &bytes.Buffer{}

	logger := slog.New(slog.NewTextHandler(logBuffer, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				return slog.Time(slog.TimeKey, time.Date(2023, 10, 1, 12, 34, 57, 0, time.UTC))
			case "duration":
				return slog.Duration("duration", 42*time.Millisecond)
			default:
				return a
			}
		},
	}))

	s := &server{logger: logger}
	loggedHandler := requestIDMiddleware(requestLogger(logger)(s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))))

	req := httptest.NewRequest("POST", "http://lin.ko/api/login", nil)
	req.Header.Set(requestIDHeader, "request-456")
	req.SetBasicAuth("frodo", "ofTheNineFingers")
	rr := httptest.NewRecorder()
	loggedHandler.ServeHTTP(rr, req)

	const expectedLogString = `time=2023-10-01T12:34:57.000Z level=INFO msg="Served request" method=POST path=/api/login request_id=request-456 client_ip=192.0.2.1:1234 duration=42ms request_body_bytes=0 response_status=200 response_body_bytes=0 user=frodo` + "\n"
	const expectedStatusCode = http.StatusOK

	if got := logBuffer.String(); got != expectedLogString {
		t.Errorf("unexpected log output:\n got: %q\n want: %q", got, expectedLogString)
	}
	if rr.Code != expectedStatusCode {
		t.Errorf("unexpected status code: got %d, want %d", rr.Code, expectedStatusCode)
	}
}

func Test_requestLoggerLogsError(t *testing.T) {
	logBuffer := &bytes.Buffer{}

	logger := slog.New(slog.NewJSONHandler(logBuffer, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.TimeKey:
				return slog.Time(slog.TimeKey, time.Date(2023, 10, 1, 12, 34, 57, 0, time.UTC))
			case "duration":
				return slog.Duration("duration", 42*time.Millisecond)
			case "error":
				return replaceAttr(groups, a)
			default:
				return a
			}
		},
	}))

	loggedHandler := requestIDMiddleware(requestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpError(r.Context(), w, http.StatusInternalServerError, io.EOF)
	})))

	req := httptest.NewRequest("GET", "http://lin.ko/api/stats", nil)
	req.Header.Set(requestIDHeader, "request-789")
	req.RemoteAddr = "192.0.2.1:1234"
	rr := httptest.NewRecorder()
	loggedHandler.ServeHTTP(rr, req)

	var got map[string]any
	if err := json.Unmarshal(logBuffer.Bytes(), &got); err != nil {
		t.Fatalf("unexpected log output: %v", err)
	}
	if got["request_id"] != "request-789" {
		t.Fatalf("unexpected request_id: %#v", got["request_id"])
	}
	errorValue, ok := got["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected structured error object, got %#v", got["error"])
	}
	if errorValue["message"] != "EOF" {
		t.Fatalf("unexpected error message: %#v", errorValue["message"])
	}
	stackTrace, ok := errorValue["stack_trace"].(string)
	if !ok || stackTrace == "" {
		t.Fatalf("expected stack_trace for io.EOF: %#v", errorValue)
	}
	if got := rr.Body.String(); got != "Internal Server Error\n" {
		t.Fatalf("unexpected response body: got %q, want %q", got, "Internal Server Error\n")
	}
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("unexpected status code: got %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}

func Test_httpErrorResponseBody(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		err        error
		wantBody   string
		wantLogged string
	}{
		{name: "unauthorized", status: http.StatusUnauthorized, err: io.EOF, wantBody: http.StatusText(http.StatusUnauthorized), wantLogged: "EOF"},
		{name: "forbidden", status: http.StatusForbidden, err: io.EOF, wantBody: http.StatusText(http.StatusForbidden), wantLogged: "EOF"},
		{name: "internal server error", status: http.StatusInternalServerError, err: io.EOF, wantBody: http.StatusText(http.StatusInternalServerError), wantLogged: "EOF"},
		{name: "bad request keeps text", status: http.StatusBadRequest, err: io.EOF, wantBody: io.EOF.Error(), wantLogged: "EOF"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			logCtx := &LogContext{}
			ctx := context.WithValue(context.Background(), LogContextKey, logCtx)
			rr := httptest.NewRecorder()

			httpError(ctx, rr, tc.status, tc.err)

			if got := rr.Body.String(); got != tc.wantBody+"\n" {
				t.Fatalf("unexpected response body: got %q, want %q", got, tc.wantBody+"\n")
			}
			if logCtx.Error == nil {
				t.Fatal("expected log context error to be set")
			}
			if got := logCtx.Error.Error(); got != tc.wantLogged {
				t.Fatalf("unexpected logged error: got %q, want %q", got, tc.wantLogged)
			}
		})
	}
}
