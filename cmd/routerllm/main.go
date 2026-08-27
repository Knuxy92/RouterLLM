package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"routerllm/internal/admin"
	"routerllm/internal/config"
	"routerllm/internal/handlers"
	"routerllm/internal/provider"
	"routerllm/internal/routers"
	"routerllm/internal/services"
	"routerllm/internal/util"
)

func main() {
	_ = util.LoadDotenv(".env")

	if len(os.Args) > 1 && os.Args[1] == "--cline-login" {
		if err := runClineLogin(); err != nil {
			log.Fatalf("cline login failed: %v", err)
		}
		return
	}

	var operationalOut io.Writer = os.Stdout
	var logFile *os.File
	logBuffer := admin.NewLogBuffer()
	if lf := os.Getenv("ROUTERLLM_LOG_FILE"); lf != "" {
		f, err := os.OpenFile(lf, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
		if err != nil {
			log.Fatalf("failed to open log file %s: %v", lf, err)
		}
		defer f.Close()
		logFile = f
		operationalOut = io.MultiWriter(os.Stdout, f, logBuffer)
	} else {
		operationalOut = io.MultiWriter(os.Stdout, logBuffer)
	}
	log.SetOutput(operationalOut)
	overviewLogger := log.New(operationalOut, "", log.LstdFlags)
	operationalLogger := log.New(operationalOut, "", log.LstdFlags)
	cfg := config.Load()

	if cfg == nil {
		overviewLogger.Fatal("failed to load routerllm.yaml — see log file for details")
	}

	if len(cfg.Providers) == 0 {
		overviewLogger.Fatal("no providers configured — add at least one provider to routerllm.yaml")
	}

	debug := os.Getenv("ROUTERLLM_DEBUG") == "true"
	advancedDebug := os.Getenv("ROUTERLLM_DEBUG_ADVANCED") == "true"
	registry := provider.NewRegistry(cfg.Providers, cfg.Routes, cfg.Cooldown)
	proxy := services.NewProxy(registry, cfg.Client, operationalLogger, debug, advancedDebug, cfg.ForceStream, cfg.ForwardClientHeaders, cfg.AllowClientHeaders, cfg.SystemPrompt)
	logRegistry(overviewLogger, registry)
	overviewLogger.Printf("loaded %d provider(s) (%d active), %d model(s), debug=%t, advanced_debug=%t, log_file=%t", registry.TotalProviders(), registry.ActiveProviders(), len(registry.AllModels()), debug, advancedDebug, logFile != nil)

	h := handlers.New(proxy)
	var auditLogger *log.Logger
	if advancedDebug && logFile != nil {
		auditLogger = log.New(logFile, "", log.LstdFlags)
	}
	reloadTracker := admin.NewReloadTracker()
	configPath := config.ConfigPath()
	applyConfig := func(next *config.Config) {
		reloaded := provider.Rebuild(next.Providers, next.Routes, next.Cooldown, proxy.Registry())
		proxy.Apply(reloaded, next.SystemPrompt)
		logRegistry(overviewLogger, reloaded)
		reloadTracker.RecordSuccess()
		overviewLogger.Printf("config reloaded: %d provider(s) (%d active), %d model(s)", reloaded.TotalProviders(), reloaded.ActiveProviders(), len(reloaded.AllModels()))
	}

	reloader := config.NewReloader(configPath, config.Hash(configPath), overviewLogger, applyConfig, reloadTracker.RecordFailure)

	adminDeps := admin.Deps{
		Registry:  proxy.Registry,
		Editor:    admin.NewEditor(configPath),
		Logs:      logBuffer,
		Reloads:   reloadTracker,
		StartedAt: time.Now(),
		Reload:    reloader.Reload,
	}

	mountAdmin := func(r chi.Router) {
		admin.Mount(r, adminDeps)
		admin.MountUI(r)
	}
	handler := routers.New(h, auditLogger, mountAdmin)

	watchCtx, stopWatcher := context.WithCancel(context.Background())
	defer stopWatcher()
	go reloader.Watch(watchCtx)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		overviewLogger.Printf("listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			overviewLogger.Fatal(err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	overviewLogger.Println("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		overviewLogger.Fatal("server forced to shutdown:", err)
	}
	overviewLogger.Println("server stopped")
}

func logRegistry(logger *log.Logger, reg *provider.Registry) {
	for _, skipped := range reg.SkippedRoutes() {
		logger.Printf("route skipped: %s", skipped)
	}
}
