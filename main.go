package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
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

func initializeLogger() (*log.Logger, *os.File) {
	// If LINKO_LOG_FILE is set, write to both the file and STDERR.
	// Otherwise, only write to STDERR.
	path := os.Getenv("LINKO_LOG_FILE")
	if path == "" {
		return log.New(os.Stderr, "", log.LstdFlags), nil
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		// If we can't open the file, fall back to STDERR and emit a message there.
		fmt.Fprintf(os.Stderr, "failed to open log file %s, using STDERR: %v\n", path, err)
		return log.New(os.Stderr, "", log.LstdFlags), nil
	}

	mw := io.MultiWriter(f, os.Stderr)
	return log.New(mw, "", log.LstdFlags), f
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
		logger.Printf("failed to create store: %v", err)
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
		logger.Printf("failed to shutdown server: %v", err)
		return 1
	}
	if serverErr != nil {
		logger.Printf("server error: %v", serverErr)
		return 1
	}
	return 0
}
