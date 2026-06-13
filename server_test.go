package main

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
	loggedHandler := requestLoggerMiddleware(dummyHandler)

	req := httptest.NewRequest("POST", "http://lin.ko/api/stats", bytes.NewBufferString("payload"))
	req.RemoteAddr = "192.0.2.1:1234"
	rr := httptest.NewRecorder()
	loggedHandler.ServeHTTP(rr, req)

	const expectedLogString = `time=2023-10-01T12:34:57.000Z level=INFO msg="Served request" method=POST path=/api/stats client_ip=192.0.2.1:1234 duration=42ms request_body_bytes=7 response_status=201 response_body_bytes=2` + "\n"
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
	loggedHandler := requestLogger(logger)(s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	req := httptest.NewRequest("POST", "http://lin.ko/api/login", nil)
	req.SetBasicAuth("frodo", "ofTheNineFingers")
	rr := httptest.NewRecorder()
	loggedHandler.ServeHTTP(rr, req)

	const expectedLogString = `time=2023-10-01T12:34:57.000Z level=INFO msg="Served request" method=POST path=/api/login client_ip=192.0.2.1:1234 duration=42ms request_body_bytes=0 response_status=200 response_body_bytes=0 user=frodo` + "\n"
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

	loggedHandler := requestLogger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		httpError(r.Context(), w, http.StatusInternalServerError, io.EOF)
	}))

	req := httptest.NewRequest("GET", "http://lin.ko/api/stats", nil)
	req.RemoteAddr = "192.0.2.1:1234"
	rr := httptest.NewRecorder()
	loggedHandler.ServeHTTP(rr, req)

	var got map[string]any
	if err := json.Unmarshal(logBuffer.Bytes(), &got); err != nil {
		t.Fatalf("unexpected log output: %v", err)
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
	if rr.Code != http.StatusInternalServerError {
		t.Errorf("unexpected status code: got %d, want %d", rr.Code, http.StatusInternalServerError)
	}
}
