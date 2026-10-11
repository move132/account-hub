package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"account-hub/internal/app"
	"account-hub/internal/platform/logging"
)

func main() {
	logger := slog.New(logging.NewColorHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	config := app.LoadConfig()
	listener, err := net.Listen("tcp", config.ListenAddress)
	if err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
	application, err := app.New(context.Background(), config, logger)
	if err != nil {
		_ = listener.Close()
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(stop)
	serveDone := make(chan error, 1)
	go func() { serveDone <- application.Run(listener) }()

	var serveErr error
	serveReturned := false
	select {
	case <-stop:
	case serveErr = <-serveDone:
		serveReturned = true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	shutdownErr := application.Shutdown(ctx)
	if !serveReturned {
		select {
		case serveErr = <-serveDone:
		case <-ctx.Done():
			serveErr = ctx.Err()
		}
	}
	if shutdownErr != nil {
		logger.Error("shutdown failed", "error", shutdownErr)
	}
	if serveErr != nil {
		logger.Error("server stopped", "error", serveErr)
	}
	if serveErr != nil || shutdownErr != nil {
		os.Exit(1)
	}
}
