package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"vless-aggregator/internal/admin"
	"vless-aggregator/internal/aggregator"
	"vless-aggregator/internal/config"
	"vless-aggregator/internal/handler"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfgPath := os.Getenv("CONFIG_PATH")
	if cfgPath == "" {
		cfgPath = "./config.json"
	}

	cfgMgr, err := config.NewManager(cfgPath)
	if err != nil {
		logger.Error("failed to load config", "path", cfgPath, "error", err)
		os.Exit(1)
	}

	agg := aggregator.New(cfgMgr, logger)

	mux := http.NewServeMux()

	// Admin panel: /admin/ and /admin/login
	adminHandler := admin.NewHandler(cfgMgr, logger)
	adminHandler.Register(mux)

	// Health probe
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"status":"ok","time":"%s"}`, time.Now().UTC().Format(time.RFC3339))
	})

	// Catch-all: proxy every path to all upstream hosts
	subHandler := handler.NewSubHandler(cfgMgr, agg, logger)
	mux.Handle("/", handler.LoggingMiddleware(logger, subHandler))

	port := cfgMgr.Get().Server.Port
	srv := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 90 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go func() {
		logger.Info("server started",
			"port", port,
			"admin", fmt.Sprintf("http://localhost:%d/admin/", port),
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
}
