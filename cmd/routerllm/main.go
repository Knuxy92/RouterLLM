package main

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
		Sessions:  admin.NewSessionStore(func() string { return os.Getenv("ROUTERLLM_ADMIN_TOKEN") }),
		Logs:      logBuffer,
		Reloads:   reloadTracker,
		StartedAt: time.Now(),
		Reload:    reloader.Reload,
	}

	// When the TLS admin port is set, the console exists only on that HTTPS
	// listener — serving it over plain HTTP on the main port would undo the
	// encryption. Default: console on the main port as before.
	var mountAdmin func(chi.Router)
	var adminTLS *http.Server
	if port := os.Getenv("ROUTERLLM_ADMIN_TLS_PORT"); port != "" {
		certPath := os.Getenv("ROUTERLLM_ADMIN_TLS_CERT")
		keyPath := os.Getenv("ROUTERLLM_ADMIN_TLS_KEY")
		if certPath == "" || keyPath == "" {
			dir := filepath.Dir(configPath)
			if certPath == "" {
				certPath = filepath.Join(dir, "admin-tls.crt")
			}
			if keyPath == "" {
				keyPath = filepath.Join(dir, "admin-tls.key")
			}
		}
		cert, err := admin.EnsureCertificate(certPath, keyPath)
		if err != nil {
			overviewLogger.Fatalf("admin TLS certificate: %v", err)
		}

		adminMux := chi.NewRouter()
		admin.Mount(adminMux, adminDeps)
		admin.MountUI(adminMux)
		adminTLS = &http.Server{
			Addr:              ":" + port,
			Handler:           adminMux,
			TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
			ReadHeaderTimeout: 10 * time.Second,
			IdleTimeout:       120 * time.Second,
		}
		go func() {
			overviewLogger.Printf("admin console (TLS) on :%s — cert %s, key %s", port, certPath, keyPath)
			if err := adminTLS.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				overviewLogger.Fatal(err)
			}
		}()
	} else {
		mountAdmin = func(r chi.Router) {
			admin.Mount(r, adminDeps)
			admin.MountUI(r)
		}
	}

	if os.Getenv("AUTHTOKEN") == "" {
		overviewLogger.Printf("v1 auth is disabled (AUTHTOKEN unset) — /v1 write endpoints accept anyone")
	}

	handler := routers.New(h, auditLogger, func() string { return os.Getenv("AUTHTOKEN") }, mountAdmin)

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
	if adminTLS != nil {
		if err := adminTLS.Shutdown(ctx); err != nil {
			overviewLogger.Fatal("admin TLS server forced to shutdown:", err)
		}
	}
	overviewLogger.Println("server stopped")
}

func logRegistry(logger *log.Logger, reg *provider.Registry) {
	for _, skipped := range reg.SkippedRoutes() {
		logger.Printf("route skipped: %s", skipped)
	}
}
