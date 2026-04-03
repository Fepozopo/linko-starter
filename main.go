package main

import (
	"context"
	"flag"
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

func run(ctx context.Context, cancel context.CancelFunc, httpPort int, dataDir string) int {
	// Standard logger writes to STDERR with DEBUG prefix and is used for internal messages
	stdLogger := log.New(os.Stderr, "DEBUG: ", log.LstdFlags)

	// Create/access the access log file; fall back to STDERR if we can't open the file
	accessFile, err := os.OpenFile("linko.access.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		stdLogger.Printf("failed to open access log, using STDERR: %v", err)
		accessFile = os.Stderr
	} else {
		// only close if we successfully opened a real file
		defer accessFile.Close()
	}
	// Access logger writes to the access log with INFO prefix and is used for request/server logs
	accessLogger := log.New(accessFile, "INFO: ", log.LstdFlags)

	st, err := store.New(dataDir, stdLogger)
	if err != nil {
		stdLogger.Printf("failed to create store: %v", err)
		return 1
	}
	s := newServer(st, httpPort, cancel, accessLogger, stdLogger)
	var serverErr error
	go func() {
		serverErr = s.start()
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.shutdown(shutdownCtx); err != nil {
		stdLogger.Printf("failed to shutdown server: %v", err)
		return 1
	}
	if serverErr != nil {
		stdLogger.Printf("server error: %v", serverErr)
		return 1
	}
	return 0
}
