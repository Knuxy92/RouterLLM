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
	logOut := os.Stdout
	if lf := os.Getenv("ROUTERLLM_LOG_FILE"); lf != "" {
		f, err := os.OpenFile(lf, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("failed to open log file %s: %v", lf, err)
		}
		defer f.Close()
		logOut = f
	}
	logger := log.New(logOut, "", log.LstdFlags)
	cfg := config.Load()
	if cfg == nil {
		logger.Fatal("failed to load routerllm.yaml — see log above for details")
	}
	if len(cfg.Providers) == 0 {
		logger.Fatal("no providers configured — add at least one provider to routerllm.yaml")
	}

	debug := os.Getenv("ROUTERLLM_DEBUG") == "true"
	registry := provider.NewRegistry(cfg.Providers, cfg.Routes, cfg.Cooldown)
	proxy := services.NewProxy(registry, cfg.Client, logger, debug, cfg.ForceStream, cfg.SystemPrompt)
	logger.Printf("loaded %d provider(s), %d model(s)", len(cfg.Providers), len(registry.AllModels()))

	h := handlers.New(proxy, registry.AllModels())
	handler := routers.New(h)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
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
