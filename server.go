package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"boot.dev/linko/internal/store"
)

type server struct {
	httpServer *http.Server
	store      *store.Store
	cancel     context.CancelFunc
	logger     *slog.Logger
}

type spyReadCloser struct {
	io.ReadCloser
	bytesRead int64
}

func (s *spyReadCloser) Read(p []byte) (int, error) {
	n, err := s.ReadCloser.Read(p)
	s.bytesRead += int64(n)
	return n, err
}

type spyResponseWriter struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

func (s *spyResponseWriter) WriteHeader(statusCode int) {
	if s.statusCode == 0 {
		s.statusCode = statusCode
	}
	s.ResponseWriter.WriteHeader(statusCode)
}

func (s *spyResponseWriter) Write(p []byte) (int, error) {
	if s.statusCode == 0 {
		s.statusCode = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(p)
	s.bytesWritten += int64(n)
	return n, err
}

func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			if r.Body == nil {
				r.Body = http.NoBody
			}
			requestBody := &spyReadCloser{ReadCloser: r.Body}
			r.Body = requestBody

			responseWriter := &spyResponseWriter{ResponseWriter: w}
			next.ServeHTTP(responseWriter, r)

			// Log structured access information.
			clientIP := r.RemoteAddr
			if host, port, err := net.SplitHostPort(r.RemoteAddr); err == nil {
				clientIP = fmt.Sprintf("%s:%s", host, port)
			}
			if responseWriter.statusCode == 0 {
				responseWriter.statusCode = http.StatusOK
			}
			logger.Info(
				"Served request",
				"method", r.Method,
				"path", r.URL.Path,
				"client_ip", clientIP,
				"duration", time.Since(start),
				"request_body_bytes", requestBody.bytesRead,
				"response_status", responseWriter.statusCode,
				"response_body_bytes", responseWriter.bytesWritten,
			)
		})
	}
}

func newServer(store *store.Store, port int, cancel context.CancelFunc, logger *slog.Logger) *server {
	mux := http.NewServeMux()

	s := &server{
		store:  store,
		cancel: cancel,
		logger: logger,
	}

	mux.HandleFunc("GET /", s.handlerIndex)
	mux.Handle("POST /api/login", s.authMiddleware(http.HandlerFunc(s.handlerLogin)))
	mux.Handle("POST /api/shorten", s.authMiddleware(http.HandlerFunc(s.handlerShortenLink)))
	mux.Handle("GET /api/stats", s.authMiddleware(http.HandlerFunc(s.handlerStats)))
	mux.Handle("GET /api/urls", s.authMiddleware(http.HandlerFunc(s.handlerListURLs)))
	mux.HandleFunc("GET /{shortCode}", s.handlerRedirect)
	mux.HandleFunc("POST /admin/shutdown", s.handlerShutdown)

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: requestLogger(s.logger)(mux),
	}
	s.httpServer = srv

	return s
}

func (s *server) start() error {
	ln, err := net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}
	s.logger.Debug("Linko is running", "addr", fmt.Sprintf("http://localhost:%d", ln.Addr().(*net.TCPAddr).Port))
	if err := s.httpServer.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *server) shutdown(ctx context.Context) error {
	s.logger.Debug("Linko is shutting down")
	return s.httpServer.Shutdown(ctx)
}

func (s *server) handlerShutdown(w http.ResponseWriter, r *http.Request) {
	if os.Getenv("ENV") == "production" {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
	go s.cancel()
}
