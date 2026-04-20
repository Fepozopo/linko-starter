package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

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

func initializeLogger() (*slog.Logger, io.Closer) {
	// If LINKO_LOG_FILE is set, write to both the file and STDERR.
	// Otherwise, only write to STDERR.
	path := os.Getenv("LINKO_LOG_FILE")
	if path == "" {
		// STDERR should include DEBUG and above.
		stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
		return slog.New(stderrHandler), nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		// If we can't open the file, fall back to STDERR and emit a message there.
		fmt.Fprintf(os.Stderr, "failed to open log file %s, using STDERR: %v\n", path, err)
		// Ensure STDERR includes DEBUG and above even when file logging fails.
		stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})
		return slog.New(stderrHandler), nil
	}

	// Wrap file writer with an 8KB buffered writer.
	bufWriter := bufio.NewWriterSize(f, 8192)

	// File handler should include INFO and above.
	fileHandler := slog.NewTextHandler(bufWriter, &slog.HandlerOptions{Level: slog.LevelInfo})
	// STDERR handler should include DEBUG and above.
	stderrHandler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})

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
