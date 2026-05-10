package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/yereke99/stone/internal/bot"
	"github.com/yereke99/stone/internal/config"
	httpserver "github.com/yereke99/stone/internal/http"
	httpHandler "github.com/yereke99/stone/internal/http/handler"
	"github.com/yereke99/stone/internal/logger"
	"github.com/yereke99/stone/internal/meta"
	"github.com/yereke99/stone/internal/storage"
	"go.uber.org/zap"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}

	zapLogger, err := logger.New(cfg.Env)
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = zapLogger.Sync()
	}()

	stateStore := storage.NewMemoryStore()
	botManager := bot.NewManager(
		stateStore,
		bot.PortfolioLinks{
			TestURL:     cfg.Portfolio.TestURL,
			BasicURL:    cfg.Portfolio.BasicURL,
			StandardURL: cfg.Portfolio.StandardURL,
		},
		zapLogger,
	)

	metaClient := meta.NewClient(cfg.Meta, zapLogger)
	healthHandler := httpHandler.NewHealthHandler(cfg.Env)
	webhookHandler := httpHandler.NewWebhookHandler(cfg.Meta, botManager, metaClient, zapLogger)

	server := &http.Server{
		Addr:              cfg.HTTPAddress(),
		Handler:           httpserver.NewRouter(healthHandler, webhookHandler, zapLogger),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	serverErrors := make(chan error, 1)
	go func() {
		zapLogger.Info("http server started", zap.String("addr", server.Addr))
		serverErrors <- server.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	select {
	case err := <-serverErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			zapLogger.Fatal("http server failed", zap.Error(err))
		}
	case sig := <-shutdown:
		zapLogger.Info("shutdown signal received", zap.String("signal", sig.String()))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		zapLogger.Error("graceful shutdown failed", zap.Error(err))
		if closeErr := server.Close(); closeErr != nil {
			zapLogger.Error("server close failed", zap.Error(closeErr))
		}
	}

	zapLogger.Info("application stopped")
}
