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
	"routerllm/internal/routing"
	"routerllm/internal/services"

	"github.com/gin-gonic/gin"
)

func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags)
	cfg := config.Load()

	if len(cfg.Providers) == 0 {
		logger.Fatal("no providers configured — create routerllm.yaml or set env vars")
	}

	rules := cfg.Routes
	if rules == nil {
		var err error
		rules, err = loadRoutesFile(cfg.RoutesFile)
		if err != nil {
			rules = routing.DefaultRules()
			logger.Printf("using default routes (%d rules)", len(rules))
		} else {
			logger.Printf("loaded %d route rules from %s", len(rules), cfg.RoutesFile)
		}
	}

	registry := provider.NewRegistry(cfg.Providers, rules, cfg.Cooldown)
	proxy := services.NewProxy(registry, cfg.Client, logger)
	logger.Printf("loaded %d provider(s), %d model(s)", len(cfg.Providers), len(registry.AllModels()))

	gin.SetMode(gin.ReleaseMode)
	h := handlers.New(proxy, registry.AllModels())
	router := routers.New(h)

	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: router,
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

func loadRoutesFile(path string) ([]routing.Rule, error) {
	if path == "" {
		return nil, os.ErrNotExist
	}
	rc, err := routing.Load(path)
	if err != nil {
		return nil, err
	}
	return rc.Routes, nil
}
