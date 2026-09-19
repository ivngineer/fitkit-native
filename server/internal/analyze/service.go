package analyze

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"fitkit/server/internal/config"
	"fitkit/server/internal/store"
)

const runTimeout = 3 * time.Minute

// Service runs analyses in the background and caches results per pin, so
// every user who saved the same pin shares one analysis.
type Service struct {
	Store *store.Store
	// PinsDir holds downloaded pin images.
	PinsDir string
	// Pipeline is nil when the server lacks provider keys.
	Pipeline *Pipeline
	Missing  []string
	Log      *slog.Logger

	mu      sync.Mutex
	running map[string]bool
}

func NewService(cfg config.Config, st *store.Store, pinsDir, cropsDir string, log *slog.Logger) *Service {
	s := &Service{Store: st, PinsDir: pinsDir, Log: log, running: map[string]bool{}}
	if cfg.DemoAnalysis {
		s.Pipeline = &Pipeline{
			Detector: DemoDetector{},
			Uploader: &LocalUploader{Dir: cropsDir, URLPrefix: "/media/crops/"},
			Searcher: DemoSearcher{},
			Demo:     true,
		}
		return s
	}

	var missing []string
	if cfg.GeminiAPIKey == "" {
		missing = append(missing, "GEMINI_API_KEY")
	}
	if cfg.ImgbbAPIKey == "" {
		missing = append(missing, "IMGBB_API_KEY")
	}
	searcher := lensSearcher(cfg)
	if searcher == nil {
		missing = append(missing, "a visual search key (SERPAPI_API_KEY, SCRAPINGDOG_API_KEY or SEARCHAPI_API_KEY)")
	}
	if len(missing) > 0 {
		s.Missing = missing
		if cfg.GeminiAPIKey != "" {
			// Detection works on its own; crops are served locally and each
			// item links to store searches until visual search is configured.
			s.Pipeline = &Pipeline{
				Detector: &Gemini{APIKey: cfg.GeminiAPIKey, Model: cfg.GeminiModel, Fallbacks: cfg.GeminiFallbackModels},
				Uploader: &LocalUploader{Dir: cropsDir, URLPrefix: "/media/crops/"},
				Searcher: SearchLinks{},
			}
		}
		return s
	}
	s.Pipeline = &Pipeline{
		Detector: &Gemini{APIKey: cfg.GeminiAPIKey, Model: cfg.GeminiModel, Fallbacks: cfg.GeminiFallbackModels},
		Uploader: &Imgbb{APIKey: cfg.ImgbbAPIKey, Expiration: 24 * 60 * 60},
		Crops:    &LocalUploader{Dir: cropsDir, URLPrefix: "/media/crops/"},
		Searcher: searcher,
	}
	return s
}

// lensSearcher uses every visual search provider that has a key, starting
// with LENS_PROVIDER, so one provider's outage or quota falls through to the
// next. It returns nil when no provider has a key.
func lensSearcher(cfg config.Config) VisualSearcher {
	keys := map[string]string{
		"serpapi": cfg.SerpAPIKey, "scrapingdog": cfg.ScrapingdogAPIKey, "searchapi": cfg.SearchAPIKey,
	}
	order := []string{cfg.LensProvider, "searchapi", "scrapingdog", "serpapi"}
	var chain FallbackSearcher
	seen := map[string]bool{}
	for _, name := range order {
		if seen[name] || keys[name] == "" {
			continue
		}
		seen[name] = true
		chain = append(chain, &LensProvider{Provider: name, APIKey: keys[name]})
	}
	switch len(chain) {
	case 0:
		return nil
	case 1:
		return chain[0]
	}
	return chain
}

type Status struct {
	Status       store.JobStatus
	Result       *Result
	ErrorCode    string
	ErrorMessage string
}

func (s *Service) Get(ctx context.Context, pinID string) (Status, error) {
	a, err := s.Store.Analysis(ctx, pinID)
	if err != nil {
		return Status{}, err
	}
	st := Status{Status: a.Status, ErrorCode: a.ErrorCode, ErrorMessage: a.ErrorMessage}
	if a.Status == store.JobDone && a.ResultJSON != "" {
		var r Result
		if err := json.Unmarshal([]byte(a.ResultJSON), &r); err != nil {
			return Status{}, err
		}
		dropExpiredCrops(&r, a.UpdatedAt)
		st.Result = &r
	}
	return st, nil
}

// imgbbLifetime is how long crops hosted for search stay online.
const imgbbLifetime = 24 * time.Hour

// dropExpiredCrops clears crop links that analyses from before local crop
// copies point at on the image host, once the host has deleted them, so the
// app shows the store's thumbnail instead of a broken image.
func dropExpiredCrops(r *Result, analyzedAt time.Time) {
	if time.Since(analyzedAt) < imgbbLifetime-time.Hour {
		return
	}
	for i := range r.Items {
		if strings.HasPrefix(r.Items[i].CropURL, "https://i.ibb.co/") {
			r.Items[i].CropURL = ""
		}
	}
}

// Start kicks off analysis unless one is running or already succeeded.
// force re-runs a finished analysis.
func (s *Service) Start(ctx context.Context, pin store.Pin, force bool) (Status, error) {
	existing, err := s.Get(ctx, pin.ID)
	if err == nil && (existing.Status == store.JobRunning || existing.Status == store.JobQueued ||
		(existing.Status == store.JobDone && !force)) {
		return existing, nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return Status{}, err
	}

	if s.Pipeline == nil {
		st := Status{Status: store.JobFailed, ErrorCode: "not_configured",
			ErrorMessage: "Item search isn't set up on the server yet (missing " + strings.Join(s.Missing, ", ") + ")."}
		return st, s.Store.PutAnalysis(ctx, store.Analysis{PinID: pin.ID, Status: st.Status, ErrorCode: st.ErrorCode, ErrorMessage: st.ErrorMessage})
	}

	s.mu.Lock()
	if s.running[pin.ID] {
		s.mu.Unlock()
		return Status{Status: store.JobRunning}, nil
	}
	s.running[pin.ID] = true
	s.mu.Unlock()

	if err := s.Store.PutAnalysis(ctx, store.Analysis{PinID: pin.ID, Status: store.JobRunning}); err != nil {
		s.done(pin.ID)
		return Status{}, err
	}
	go s.run(pin)
	return Status{Status: store.JobRunning}, nil
}

func (s *Service) done(pinID string) {
	s.mu.Lock()
	delete(s.running, pinID)
	s.mu.Unlock()
}

func (s *Service) run(pin store.Pin) {
	defer s.done(pin.ID)
	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	defer cancel()
	log := s.Log.With("pin", pin.ID)

	record := store.Analysis{PinID: pin.ID}
	result, err := s.analyze(ctx, pin)
	if err != nil {
		log.Warn("analysis failed", "err", err)
		record.Status, record.ErrorCode = store.JobFailed, "analysis_failed"
		record.ErrorMessage = "We couldn't break down this pin. Try again in a moment."
		if errors.Is(err, ErrNoItems) {
			record.ErrorCode, record.ErrorMessage = "no_items", "We didn't spot any shoppable items in this pin."
		}
	} else {
		data, _ := json.Marshal(result)
		record.Status, record.ResultJSON = store.JobDone, string(data)
		log.Info("analysis finished", "items", len(result.Items))
	}
	if err := s.Store.PutAnalysis(context.Background(), record); err != nil {
		log.Error("store analysis", "err", err)
	}
}

func (s *Service) analyze(ctx context.Context, pin store.Pin) (Result, error) {
	img, err := os.ReadFile(filepath.Join(s.PinsDir, pin.ImageFile))
	if err != nil {
		return Result{}, err
	}
	return s.Pipeline.Run(ctx, img, http.DetectContentType(img))
}
