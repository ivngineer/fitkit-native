package importer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"fitkit/server/internal/pinterest"
	"fitkit/server/internal/store"
	"fitkit/server/internal/testutil"
)

type fakeDiscoverer struct {
	pins []pinterest.Pin
	err  error
}

func (f fakeDiscoverer) Discover(_ context.Context, _ string, limit int) ([]pinterest.Pin, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.pins[:min(limit, len(f.pins))], nil
}

func setup(t *testing.T, d Discoverer) (*Importer, *store.Store, *httptest.Server, *atomic.Int32) {
	t.Helper()
	st := testutil.OpenStore(t)
	img := testutil.JPEG(t, 120, 180)
	var downloads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "broken") {
			http.Error(w, "gone", http.StatusNotFound)
			return
		}
		downloads.Add(1)
		w.Write(img)
	}))
	t.Cleanup(srv.Close)
	im := &Importer{Store: st, Discoverer: d, MediaDir: t.TempDir(), Limit: 50}
	if err := im.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	return im, st, srv, &downloads
}

func createUser(t *testing.T, st *store.Store, email, handle string) store.User {
	t.Helper()
	u, err := st.CreateUser(context.Background(), email, "hash")
	if err != nil {
		t.Fatal(err)
	}
	if handle != "" {
		if err := st.SetPinterestUsername(context.Background(), u.ID, handle); err != nil {
			t.Fatal(err)
		}
		u.PinterestUsername = handle
	}
	return u
}

func TestImportDownloadsDedupesAndReuses(t *testing.T) {
	disc := &fakeDiscoverer{}
	im, st, srv, downloads := setup(t, disc)
	disc.pins = []pinterest.Pin{
		{ID: "p1", Title: "Denim jacket", ImageURL: srv.URL + "/p1.jpg", DominantColor: "#112233"},
		{ID: "p2", BoardName: "Fall fits", ImageURL: srv.URL + "/p2.jpg"},
		{ID: "p3", Title: "Broken", ImageURL: srv.URL + "/broken.jpg"},
	}
	ctx := context.Background()

	alice := createUser(t, st, "alice@example.com", "alice")
	job, _, err := st.CreateOrReuseImportJob(ctx, alice.ID, "alice")
	if err != nil {
		t.Fatal(err)
	}
	im.Run(ctx, job.ID)

	job, _ = st.ImportJob(ctx, job.ID)
	if job.Status != store.JobDone || job.Discovered != 3 || job.Imported != 2 || job.Reused != 0 || job.Failed != 1 {
		t.Fatalf("alice job = %+v", job)
	}
	pins, _ := st.SavedPins(ctx, alice.ID, 0, 10)
	if len(pins) != 2 || pins[0].PinterestID != "p1" || pins[1].PinterestID != "p2" {
		t.Fatalf("saved pins out of order: %+v", pins)
	}
	if pins[0].Width != 120 || pins[0].Height != 180 || pins[0].AuthorID != store.PinterestAuthorID {
		t.Errorf("pin metadata wrong: %+v", pins[0])
	}
	if pins[1].Title != "Fall fits" {
		t.Errorf("untitled pin should use board name, got %q", pins[1].Title)
	}
	if _, err := os.Stat(filepath.Join(im.MediaDir, pins[0].ImageFile)); err != nil {
		t.Errorf("image not written: %v", err)
	}
	if n, _ := st.CountEmbeddingJobs(ctx); n != 2 {
		t.Errorf("embedding jobs = %d, want 2", n)
	}

	// A second user saving the same pins reuses them without downloading.
	before := downloads.Load()
	bob := createUser(t, st, "bob@example.com", "bob")
	job2, _, _ := st.CreateOrReuseImportJob(ctx, bob.ID, "bob")
	im.Run(ctx, job2.ID)
	job2, _ = st.ImportJob(ctx, job2.ID)
	if job2.Reused != 2 || job2.Imported != 0 || job2.Failed != 1 {
		t.Fatalf("bob job = %+v", job2)
	}
	if downloads.Load() != before {
		t.Errorf("reused pins should not be downloaded again")
	}
	if n, _ := st.CountEmbeddingJobs(ctx); n != 2 {
		t.Errorf("reuse should not queue embeddings, got %d", n)
	}
}

func TestImportRecordsTerminalAndRetryableFailures(t *testing.T) {
	cases := []struct {
		err       error
		code      string
		retryable bool
	}{
		{&pinterest.Error{Code: pinterest.CodePrivateProfile, Message: "private"}, "private_profile", false},
		{&pinterest.Error{Code: pinterest.CodeRateLimited, Message: "slow down"}, "rate_limited", true},
		{&pinterest.Error{Code: pinterest.CodeNoSaves, Message: "empty"}, "no_saves", false},
	}
	for _, tc := range cases {
		im, st, _, _ := setup(t, fakeDiscoverer{err: tc.err})
		u := createUser(t, st, "u@example.com", "u")
		job, _, _ := st.CreateOrReuseImportJob(context.Background(), u.ID, "u")
		im.Run(context.Background(), job.ID)
		job, _ = st.ImportJob(context.Background(), job.ID)
		if job.Status != store.JobFailed || job.ErrorCode != tc.code || job.Retryable != tc.retryable {
			t.Errorf("job = %+v; want %s retryable=%v", job, tc.code, tc.retryable)
		}
	}
}

func TestEnqueueReusesActiveJobAndRequiresHandle(t *testing.T) {
	im, st, _, _ := setup(t, fakeDiscoverer{})
	ctx := context.Background()
	noHandle := createUser(t, st, "a@example.com", "")
	if _, err := im.Enqueue(ctx, noHandle); err != ErrNoPinterestHandle {
		t.Fatalf("err = %v", err)
	}

	u := createUser(t, st, "b@example.com", "bee")
	first, created, _ := st.CreateOrReuseImportJob(ctx, u.ID, "bee")
	if !created {
		t.Fatal("first job should be created")
	}
	second, created, _ := st.CreateOrReuseImportJob(ctx, u.ID, "bee")
	if created || second.ID != first.ID {
		t.Fatalf("active job should be reused: %v %v", first.ID, second.ID)
	}
}
