package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"routerllm/internal/config"
	"routerllm/internal/handlers"
	"routerllm/internal/provider"
	"routerllm/internal/routers"
	"routerllm/internal/services"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)
	cfg := config.Load()
	if cfg == nil {
		logger.Fatal("no config found — create routerllm.yaml")
	}
	if len(cfg.Providers) == 0 {
		logger.Fatal("no providers configured — add at least one provider to routerllm.yaml")
	}

	registry := provider.NewRegistry(cfg.Providers, cfg.Routes, cfg.Cooldown)
	proxy := services.NewProxy(registry, cfg.Client, logger)
	logger.Printf("loaded %d provider(s), %d model(s)", len(cfg.Providers), len(registry.AllModels()))

	h := handlers.New(proxy, registry.AllModels())
	handler := routers.New(h)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: handler,
	}

	go func() {
		logger.Printf("listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Println("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("server forced to shutdown:", err)
	}
	logger.Println("server stopped")
}
