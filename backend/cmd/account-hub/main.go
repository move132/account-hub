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
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if err := application.Shutdown(ctx); err != nil {
			logger.Error("shutdown failed", "error", err)
		}
	}()

	if err := application.Run(listener); err != nil {
		logger.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
