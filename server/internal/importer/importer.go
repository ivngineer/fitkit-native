// Package importer runs background jobs that pull a user's public Pinterest
// saves into Fitkit.
package importer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "golang.org/x/image/webp"

	"fitkit/server/internal/pinterest"
	"fitkit/server/internal/store"
)

const maxImageBytes = 25 << 20

var ErrNoPinterestHandle = errors.New("add your Pinterest username before importing")

type Discoverer interface {
	Discover(ctx context.Context, username string, limit int) ([]pinterest.Pin, error)
}

type Importer struct {
	Store      *store.Store
	Discoverer Discoverer
	HTTP       *http.Client
	MediaDir   string
	Limit      int
	Workers    int
	// PinConcurrency bounds parallel image downloads within one job.
	PinConcurrency int
	Log            *slog.Logger

	queue chan string
	wg    sync.WaitGroup
}

// Start launches workers and resumes jobs interrupted by a restart.
func (im *Importer) Start(ctx context.Context) error {
	if im.Workers <= 0 {
		im.Workers = 1
	}
	if im.PinConcurrency <= 0 {
		im.PinConcurrency = 4
	}
	if im.HTTP == nil {
		im.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	if im.Log == nil {
		im.Log = slog.Default()
	}
	if err := os.MkdirAll(im.MediaDir, 0o755); err != nil {
		return err
	}
	im.queue = make(chan string, 1024)
	for range im.Workers {
		im.wg.Go(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case id := <-im.queue:
					im.Run(ctx, id)
				}
			}
		})
	}
	ids, err := im.Store.ActiveImportJobIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		im.queue <- id
	}
	return nil
}

func (im *Importer) Wait() { im.wg.Wait() }

// Enqueue creates an import job for the user or returns the one in flight.
func (im *Importer) Enqueue(ctx context.Context, user store.User) (store.ImportJob, error) {
	if user.PinterestUsername == "" {
		return store.ImportJob{}, ErrNoPinterestHandle
	}
	job, created, err := im.Store.CreateOrReuseImportJob(ctx, user.ID, user.PinterestUsername)
	if err != nil || !created {
		return job, err
	}
	select {
	case im.queue <- job.ID:
	default:
		go func() { im.queue <- job.ID }()
	}
	return job, nil
}

// Run executes one job to completion. Exported for tests.
func (im *Importer) Run(ctx context.Context, jobID string) {
	job, err := im.Store.ImportJob(ctx, jobID)
	if err != nil || !job.Active() {
		return
	}
	log := im.Log.With("job", job.ID, "pinterest", job.PinterestUsername)
	if err := im.Store.MarkImportRunning(ctx, job.ID); err != nil {
		log.Error("mark running", "err", err)
		return
	}

	pins, err := im.Discoverer.Discover(ctx, job.PinterestUsername, im.Limit)
	if err != nil {
		code, retryable := "upstream_error", true
		var pe *pinterest.Error
		if errors.As(err, &pe) {
			code, retryable = string(pe.Code), pe.Retryable()
		}
		if ctx.Err() != nil {
			return // shutting down; job resumes on next start
		}
		log.Warn("discovery failed", "code", code, "err", err)
		im.finish(job.ID, store.JobFailed, code, err.Error(), retryable)
		return
	}

	var (
		mu                       sync.Mutex
		imported, reused, failed int
		sem                      = make(chan struct{}, im.PinConcurrency)
		wg                       sync.WaitGroup
		base                     = time.Now()
	)
	_ = im.Store.UpdateImportProgress(ctx, job.ID, len(pins), 0, 0, 0)

	for i, p := range pins {
		// Preserve Pinterest's newest-first order in saved_at.
		savedAt := base.Add(-time.Duration(i) * time.Millisecond)
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			wasReused, err := im.ingest(ctx, job.UserID, p, savedAt)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err != nil:
				failed++
				log.Warn("pin ingest failed", "pin", p.ID, "err", err)
			case wasReused:
				reused++
			default:
				imported++
			}
			_ = im.Store.UpdateImportProgress(ctx, job.ID, len(pins), imported, reused, failed)
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}

	if imported+reused == 0 && failed > 0 {
		im.finish(job.ID, store.JobFailed, "ingest_failed", "None of your pins could be downloaded. Try again later.", true)
		return
	}
	log.Info("import finished", "discovered", len(pins), "imported", imported, "reused", reused, "failed", failed)
	im.finish(job.ID, store.JobDone, "", "", false)
}

func (im *Importer) finish(id string, status store.JobStatus, code, msg string, retryable bool) {
	// Use a fresh context so a finished job is recorded even during shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := im.Store.FinishImportJob(ctx, id, status, code, msg, retryable); err != nil {
		im.Log.Error("finish job", "job", id, "err", err)
	}
}

// ingest stores one discovered pin, reusing an existing copy when another
// import already downloaded it.
func (im *Importer) ingest(ctx context.Context, userID string, p pinterest.Pin, savedAt time.Time) (reused bool, err error) {
	existing, err := im.Store.PinByPinterestID(ctx, p.ID)
	if err == nil {
		return true, im.Store.SavePinForUser(ctx, userID, existing.ID, savedAt)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}

	data, err := im.download(ctx, p.ImageURL)
	if err != nil {
		return false, err
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return false, fmt.Errorf("decode image: %w", err)
	}

	pin := &store.Pin{
		ID:             store.NewID(),
		PinterestID:    p.ID,
		Title:          firstNonEmpty(p.Title, p.BoardName, "Saved pin"),
		Description:    p.Description,
		Link:           p.Link,
		ImageSourceURL: p.ImageURL,
		Width:          cfg.Width,
		Height:         cfg.Height,
		DominantColor:  p.DominantColor,
	}
	pin.ImageFile = pin.ID + "." + extension(format)
	path := filepath.Join(im.MediaDir, pin.ImageFile)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return false, err
	}

	switch err := im.Store.InsertPin(ctx, pin); {
	case errors.Is(err, store.ErrConflict):
		// Another job stored this pin concurrently; use theirs.
		os.Remove(path)
		existing, err := im.Store.PinByPinterestID(ctx, p.ID)
		if err != nil {
			return false, err
		}
		return true, im.Store.SavePinForUser(ctx, userID, existing.ID, savedAt)
	case err != nil:
		os.Remove(path)
		return false, err
	}
	return false, im.Store.SavePinForUser(ctx, userID, pin.ID, savedAt)
}

func (im *Importer) download(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := im.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("image download: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, errors.New("image too large")
	}
	return data, nil
}

func extension(format string) string {
	if format == "jpeg" {
		return "jpg"
	}
	return format
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
