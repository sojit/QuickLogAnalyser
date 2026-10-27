package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"quickanalyser/internal/appinfo"
	"quickanalyser/internal/config"
	"quickanalyser/internal/diag"
	"quickanalyser/internal/server"
	"quickanalyser/internal/storage"
)

func main() {
	dataDir := flag.String("data-dir", "", "absolute working storage root")
	portable := flag.Bool("portable", false, "use QuickAnalyserData beside the executable")
	noBrowser := flag.Bool("no-browser", false, "do not auto-open a browser")
	logLevel := flag.String("log-level", "info", "diagnostics level: info|debug")
	debugMinutes := flag.Int("debug-minutes", 15, "DEBUG session lifetime in minutes when --log-level=debug")
	flag.Parse()

	fmt.Printf("%s %s · %s\n", appinfo.ProductName, appinfo.Version(), appinfo.DeveloperDisplay)

	cfg := diag.DefaultConfig()
	if *debugMinutes > 0 {
		cfg.DebugTTL = time.Duration(*debugMinutes) * time.Minute
	}
	log := diag.Configure(cfg)
	log.SetLevel(*logLevel)

	root, needsSetup, err := storage.ResolveStartup(*dataDir, *portable)
	if err != nil {
		fmt.Fprintf(os.Stderr, "storage: %v\n", err)
		os.Exit(1)
	}
	defer root.Close()
	_ = log.AttachRoot(root.Root)
	log.Info("startup.ready", map[string]any{"needs_setup": needsSetup})

	limits := config.DefaultLimits()
	srv := server.New(root, needsSetup, limits)

	go func() {
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		<-ch
		log.Info("shutdown.signal", map[string]any{})
		_ = srv.Shutdown()
		_ = log.Close()
		root.Close()
		os.Exit(0)
	}()

	if err := srv.ListenAndServe(!*noBrowser); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "server: %v\n", err)
		os.Exit(1)
	}
}
