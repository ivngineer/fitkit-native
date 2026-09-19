// Command fitkitd runs the Fitkit API server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"fitkit/server/internal/analyze"
	"fitkit/server/internal/api"
	"fitkit/server/internal/config"
	"fitkit/server/internal/importer"
	"fitkit/server/internal/pinterest"
	"fitkit/server/internal/shop"
	"fitkit/server/internal/store"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg := config.Load()
	mediaDir := filepath.Join(cfg.DataDir, "media")
	if err := api.EnsureMediaDirs(mediaDir); err != nil {
		return err
	}
	st, err := store.Open(filepath.Join(cfg.DataDir, "fitkit.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := st.ResetStaleAnalyses(ctx); err != nil {
		return err
	}

	imp := &importer.Importer{
		Store:      st,
		Discoverer: pinterest.NewClient(cfg.PinterestBaseURL),
		MediaDir:   filepath.Join(mediaDir, "pins"),
		Limit:      cfg.ImportPinLimit,
		Workers:    cfg.ImportWorkers,
		Log:        log,
	}
	if err := imp.Start(ctx); err != nil {
		return err
	}

	analysis := analyze.NewService(cfg, st, filepath.Join(mediaDir, "pins"), filepath.Join(mediaDir, "crops"), log)
	switch {
	case cfg.DemoAnalysis:
		log.Warn("analysis running in DEMO mode; results are placeholders")
	case analysis.Pipeline == nil:
		log.Warn("analysis disabled until API keys are set", "missing", analysis.Missing)
	case len(analysis.Missing) > 0:
		log.Warn("visual search not configured; items get store search links without prices", "missing", analysis.Missing)
	}

	srv := &http.Server{
		Addr: cfg.Addr,
		Handler: (&api.Server{
			Store: st, Importer: imp, Analysis: analysis, MediaDir: mediaDir, SessionTTL: cfg.SessionTTL, Log: log,
			Shop: &shop.Service{
				Cache:     st,
				Affiliate: shop.Affiliate{AmazonTags: cfg.AmazonTags, AliExpressKey: cfg.AliExpressKey, SkimlinksID: cfg.SkimlinksID},
				ShopPay:   cfg.ShopPay,
				Demo:      cfg.DemoAnalysis,
			},
		}).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	errc := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdown)
	imp.Wait()
	return err
}
