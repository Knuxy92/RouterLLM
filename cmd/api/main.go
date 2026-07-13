package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"agentrouter/internal/config"
	"agentrouter/internal/handlers"
	"agentrouter/internal/provider"
	"agentrouter/internal/routers"
	"agentrouter/internal/services"
	"agentrouter/internal/util"

	"github.com/gin-gonic/gin"
)

func main() {
	_ = util.LoadDotenv(".env")

	cfg := config.Load()
	logger := log.New(os.Stdout, "", log.LstdFlags)

	if len(cfg.Providers) == 0 {
		logger.Fatal("no providers configured (set AGENT_ROUTER_API_KEY, OPENCODE_API_KEY, ALIBABA_API_KEY, or FREEMODEL_API_KEY)")
	}

	registry := provider.NewRegistry(cfg.Providers, cfg.Cooldown)
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
