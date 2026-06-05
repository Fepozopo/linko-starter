package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	pkgerr "github.com/pkg/errors"

	"boot.dev/linko/internal/build"
	"boot.dev/linko/internal/linkoerr"
	"boot.dev/linko/internal/store"
)

// loggers are created in run() and injected into components

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	httpPort := flag.Int("port", 8899, "port to listen on")
	dataDir := flag.String("data", "./data", "directory to store data")
	flag.Parse()

	status := run(ctx, cancel, *httpPort, *dataDir)
	cancel()
	os.Exit(status)
}

// bufferedFile wraps an os.File and a bufio.Writer so we can flush on Close.
type bufferedFile struct {
	f *os.File
	w *bufio.Writer
}

func (bf *bufferedFile) Close() error {
	var flushErr error
	if bf.w != nil {
		flushErr = bf.w.Flush()
	}
	closeErr := bf.f.Close()

	// Prefer returning the flush error if present, but include both if both exist.
	if flushErr != nil {
		if closeErr != nil {
			return fmt.Errorf("flush error: %v; close error: %v", flushErr, closeErr)
		}
		return flushErr
	}
	return closeErr
}

// stackTracer extracts stack traces from errors wrapped with pkg/errors.
type stackTracer interface {
	error
	StackTrace() pkgerr.StackTrace
}

// multiError represents an error that wraps multiple underlying errors via Unwrap() []error
type multiError interface {
	error
	Unwrap() []error
}

// errorAttrs builds a slice of slog attributes for a single error. It includes:
// - a "message" attribute containing the error message
// - any structured attributes extracted via linkoerr.Attrs
// - a "stack_trace" attribute if the error implements stackTracer
func errorAttrs(err error) []slog.Attr {
	attrs := []slog.Attr{
		{
			Key:   "message",
			Value: slog.StringValue(err.Error()),
		},
	}

	// Include any structured attributes attached via linkoerr.
	attrs = append(attrs, linkoerr.Attrs(err)...)

	// If this error has a stack trace, include it after the other attrs.
	if st, ok := errors.AsType[stackTracer](err); ok {
		attrs = append(attrs, slog.Attr{
			Key:   "stack_trace",
			Value: slog.StringValue(fmt.Sprintf("%+v", st.StackTrace())),
		})
	}
	return attrs
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if a.Key != "error" {
		return a
	}
	err, ok := a.Value.Any().(error)
	if !ok {
		return a
	}

	// If this is a multi-error (errors.Join or similar), unwrap and present each
	// underlying error as a separate numbered entry inside a top-level "errors" group.
	if me, ok := errors.AsType[multiError](err); ok {
		underlying := me.Unwrap()
		var grouped []slog.Attr
		for i, ue := range underlying {
			key := fmt.Sprintf("error_%d", i+1)
			grouped = append(grouped, slog.GroupAttrs(key, errorAttrs(ue)...))
		}
		return slog.GroupAttrs("errors", grouped...)
	}

	// Single error: render as a single "error" group.
	return slog.GroupAttrs("error", errorAttrs(err)...)
}

func initializeLogger() (*slog.Logger, io.Closer) {
	// If LINKO_LOG_FILE is set, write to both the file and STDERR.
	// Otherwise, only write to STDERR.
	path := os.Getenv("LINKO_LOG_FILE")
	if path == "" {
		// STDERR should include DEBUG and above.
		stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: replaceAttr})
		return slog.New(stderrHandler), nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		// If we can't open the file, fall back to STDERR and emit a message there.
		fmt.Fprintf(os.Stderr, "failed to open log file %s, using STDERR: %v\n", path, err)
		// Ensure STDERR includes DEBUG and above even when file logging fails.
		stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: replaceAttr})
		return slog.New(stderrHandler), nil
	}

	// Wrap file writer with an 8KB buffered writer.
	bufWriter := bufio.NewWriterSize(f, 8192)

	// File handler should include INFO and above. Use JSON for file logs.
	fileHandler := slog.NewJSONHandler(bufWriter, &slog.HandlerOptions{Level: slog.LevelInfo, ReplaceAttr: replaceAttr})
	// STDERR handler should include DEBUG and above.
	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug, ReplaceAttr: replaceAttr})

	// Combine handlers so records are directed to the appropriate outputs
	// depending on their level.
	multi := slog.NewMultiHandler(fileHandler, stderrHandler)
	logger := slog.New(multi)

	return logger, &bufferedFile{f: f, w: bufWriter}
}

func run(ctx context.Context, cancel context.CancelFunc, httpPort int, dataDir string) int {
	// Initialize a single logger used throughout the app.
	logger, lf := initializeLogger()
	if lf != nil {
		// only close if we successfully opened a real file
		defer lf.Close()
	}

	logger = logger.With(
		slog.String("git_sha", build.GitSHA),
		slog.String("build_time", build.BuildTime),
	)

	st, err := store.New(dataDir, logger)
	if err != nil {
		logger.Error("failed to create store", "error", err)
		return 1
	}
	// Use the same logger for both access and internal logging.
	s := newServer(st, httpPort, cancel, logger)
	var serverErr error
	go func() {
		serverErr = s.start()
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.shutdown(shutdownCtx); err != nil {
		logger.Error("failed to shutdown server", "error", err)
		return 1
	}
	if serverErr != nil {
		logger.Error("server error", "error", serverErr)
		return 1
	}
	return 0
}
