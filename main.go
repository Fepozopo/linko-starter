package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/lmittmann/tint"
	pkgerr "github.com/pkg/errors"
	lumberjack "gopkg.in/natefinch/lumberjack.v2"

	"boot.dev/linko/internal/build"
	"boot.dev/linko/internal/linkoerr"
	internallogging "boot.dev/linko/internal/logging"
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

type rotatingFile struct {
	logger *lumberjack.Logger
}

func (rf *rotatingFile) Close() error {
	if rf.logger == nil {
		return nil
	}
	return rf.logger.Close()
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

var sensitiveKeys = []string{
	"password",
	"key",
	"apikey",
	"secret",
	"pin",
	"creditcardno",
	"user",
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

func redactURLPassword(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if parsed.User == nil {
		return raw
	}
	if _, ok := parsed.User.Password(); !ok {
		return raw
	}
	parsed.User = url.UserPassword(parsed.User.Username(), "[REDACTED]")
	return strings.ReplaceAll(parsed.String(), "%5BREDACTED%5D", "[REDACTED]")
}

func replaceAttr(groups []string, a slog.Attr) slog.Attr {
	if slices.Contains(sensitiveKeys, a.Key) {
		return slog.String(a.Key, "[REDACTED]")
	}

	if a.Value.Kind() == slog.KindString {
		redacted := redactURLPassword(a.Value.String())
		if redacted != a.Value.String() {
			return slog.String(a.Key, redacted)
		}
	}

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
	// If LINKO_LOG_FILE is set, write to both the rotating file and STDERR.
	// Otherwise, only write to STDERR.
	logFile := os.Getenv("LINKO_LOG_FILE")
	if logFile == "" {
		return slog.New(internallogging.NewStderrHandler(&tint.Options{Level: slog.LevelDebug, ReplaceAttr: replaceAttr})), nil
	}

	rotatingLogger := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    1,
		MaxAge:     28,
		MaxBackups: 10,
		LocalTime:  false,
		Compress:   true,
	}

	// File handler should include INFO and above. Use JSON for file logs.
	fileHandler := slog.NewJSONHandler(rotatingLogger, &slog.HandlerOptions{ReplaceAttr: replaceAttr})
	// STDERR handler should include DEBUG and above.
	stderrHandler := internallogging.NewStderrHandler(&tint.Options{Level: slog.LevelDebug, ReplaceAttr: replaceAttr})

	// Combine handlers so records are directed to the appropriate outputs
	// depending on their level.
	multi := slog.NewMultiHandler(fileHandler, stderrHandler)
	logger := slog.New(multi)

	return logger, &rotatingFile{logger: rotatingLogger}
}

func run(ctx context.Context, cancel context.CancelFunc, httpPort int, dataDir string) int {
	// Capture build info and environment for structured logging.
	env := os.Getenv("ENV")
	hostname, _ := os.Hostname()

	// Initialize a single logger used throughout the app.
	logger, lf := initializeLogger()
	if lf != nil {
		// Only close if file logging is enabled.
		defer lf.Close()
	}

	logger = logger.With(
		slog.String("git_sha", build.GitSHA),
		slog.String("build_time", build.BuildTime),
		slog.String("env", env),
		slog.String("hostname", hostname),
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
