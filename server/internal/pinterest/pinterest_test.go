package pinterest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNormalizeHandle(t *testing.T) {
	valid := map[string]string{
		"JaneDoe":                                     "janedoe",
		"  @jane_doe ":                                "jane_doe",
		"pinterest.com/jane_doe":                      "jane_doe",
		"https://www.pinterest.com/jane_doe/":         "jane_doe",
		"https://www.pinterest.com/jane_doe/_saved/":  "jane_doe",
		"https://uk.pinterest.co.uk/jane_doe/boards/": "jane_doe",
		"http://pinterest.fr/Jane123?invite_code=abc": "jane123",
	}
	for in, want := range valid {
		got, err := NormalizeHandle(in)
		if err != nil || got != want {
			t.Errorf("NormalizeHandle(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	invalid := map[string]error{
		"":                                   ErrEmptyHandle,
		"   ":                                ErrEmptyHandle,
		"ab":                                 ErrInvalidHandle,
		"jane doe":                           ErrInvalidHandle,
		"jane.doe!":                          ErrInvalidHandle,
		"https://www.pinterest.com/pin/123/": ErrNotProfileURL,
		"https://example.com/jane_doe":       ErrNotProfileURL,
		"https://www.pinterest.com/":         ErrNotProfileURL,
		"https://pinterest.com/search/pins/": ErrNotProfileURL,
	}
	for in, want := range invalid {
		if _, err := NormalizeHandle(in); !errors.Is(err, want) {
			t.Errorf("NormalizeHandle(%q) error = %v; want %v", in, err, want)
		}
	}
}

// fakePinterest serves the resource endpoints Discover walks.
type fakePinterest struct {
	profileStatus int
	private       bool
	saves         [][]map[string]any // pages
	boards        []map[string]any
	boardPins     map[string][]map[string]any
	rateLimitAll  bool
	calls         map[string]int
}

func pin(id, title string) map[string]any {
	return map[string]any{
		"id": id, "type": "pin", "title": title, "dominant_color": "#abcdef",
		"images": map[string]any{
			"236x": map[string]any{"url": "https://i.pinimg.com/236x/" + id + ".jpg", "width": 236, "height": 400},
			"orig": map[string]any{"url": "https://i.pinimg.com/originals/" + id + ".jpg", "width": 1000, "height": 1500},
		},
	}
}

func (f *fakePinterest) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/resource/"), "/get/")
	f.calls[name]++
	if f.rateLimitAll {
		w.WriteHeader(http.StatusTooManyRequests)
		return
	}
	var payload struct {
		Options struct {
			BoardID   string   `json:"board_id"`
			Bookmarks []string `json:"bookmarks"`
		} `json:"options"`
	}
	_ = json.Unmarshal([]byte(r.URL.Query().Get("data")), &payload)
	page := 0
	if len(payload.Options.Bookmarks) > 0 {
		fmt.Sscanf(payload.Options.Bookmarks[0], "page-%d", &page)
	}
	respond := func(data any, bookmark string) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"resource_response": map[string]any{"status": "success", "data": data, "bookmark": bookmark},
		})
	}

	switch name {
	case "UserResource":
		if f.profileStatus == http.StatusNotFound {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"resource_response": map[string]any{
				"status": "failure", "error": map[string]any{"status": 404, "message": "User not found."}}})
			return
		}
		respond(map[string]any{"username": "jane", "pin_count": 3, "is_private_profile": f.private}, "")
	case "UserPinsResource":
		if page >= len(f.saves) {
			respond([]any{}, "-end-")
			return
		}
		next := "-end-"
		if page+1 < len(f.saves) {
			next = fmt.Sprintf("page-%d", page+1)
		}
		respond(f.saves[page], next)
	case "BoardsResource":
		respond(f.boards, "-end-")
	case "BoardFeedResource":
		respond(f.boardPins[payload.Options.BoardID], "-end-")
	default:
		http.NotFound(w, r)
	}
}

func newTestClient(t *testing.T, f *fakePinterest) *Client {
	t.Helper()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL)
	c.Backoff = func(int) time.Duration { return time.Millisecond }
	return c
}

func TestDiscoverWalksSavesFeedWithPaginationAndDedup(t *testing.T) {
	f := &fakePinterest{saves: [][]map[string]any{
		{pin("1", "Jacket"), pin("2", ""), {"id": "story-1", "type": "story", "title": map[string]any{"format": "x"}}},
		{pin("2", "dupe"), pin("3", "Boots")},
	}}
	pins, err := newTestClient(t, f).Discover(context.Background(), "jane", 10)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, p := range pins {
		ids = append(ids, p.ID)
	}
	if strings.Join(ids, ",") != "1,2,3" {
		t.Fatalf("ids = %v", ids)
	}
	if pins[0].ImageURL != "https://i.pinimg.com/originals/1.jpg" || pins[0].Width != 1000 {
		t.Errorf("expected original image, got %+v", pins[0])
	}
	if f.calls["BoardsResource"] != 0 {
		t.Error("should not fall back to boards when saves feed has pins")
	}
}

func TestDiscoverRespectsLimit(t *testing.T) {
	f := &fakePinterest{saves: [][]map[string]any{{pin("1", "a"), pin("2", "b")}, {pin("3", "c")}}}
	pins, err := newTestClient(t, f).Discover(context.Background(), "jane", 2)
	if err != nil || len(pins) != 2 {
		t.Fatalf("pins = %d, err = %v", len(pins), err)
	}
}

func TestDiscoverFallsBackToBoards(t *testing.T) {
	f := &fakePinterest{
		boards: []map[string]any{
			{"id": "b1", "name": "Fall fits", "url": "/jane/fall-fits/", "type": "board"},
			{"id": "b2", "name": "Shoes", "url": "/jane/shoes/", "type": "board"},
		},
		boardPins: map[string][]map[string]any{
			"b1": {pin("10", ""), pin("11", "Coat")},
			"b2": {pin("11", "Coat"), pin("12", "")},
		},
	}
	pins, err := newTestClient(t, f).Discover(context.Background(), "jane", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 3 {
		t.Fatalf("want 3 unique pins, got %d", len(pins))
	}
	if pins[0].Title != "Fall fits" || pins[2].Title != "Shoes" {
		t.Errorf("untitled pins should use board name: %q, %q", pins[0].Title, pins[2].Title)
	}
}

func TestDiscoverErrors(t *testing.T) {
	cases := []struct {
		name      string
		f         *fakePinterest
		code      ErrorCode
		retryable bool
	}{
		{"bad handle", &fakePinterest{profileStatus: http.StatusNotFound}, CodeProfileNotFound, false},
		{"private", &fakePinterest{private: true}, CodePrivateProfile, false},
		{"no saves", &fakePinterest{}, CodeNoSaves, false},
		{"rate limited", &fakePinterest{rateLimitAll: true}, CodeRateLimited, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := newTestClient(t, tc.f).Discover(context.Background(), "jane", 10)
			var pe *Error
			if !errors.As(err, &pe) || pe.Code != tc.code || pe.Retryable() != tc.retryable {
				t.Fatalf("err = %v; want code %s retryable %v", err, tc.code, tc.retryable)
			}
		})
	}
}

func TestRateLimitIsRetried(t *testing.T) {
	f := &fakePinterest{rateLimitAll: true}
	c := newTestClient(t, f)
	c.MaxRetries = 2
	_, _ = c.Profile(context.Background(), "jane")
	if f.calls["UserResource"] != 3 {
		t.Fatalf("expected 1 call + 2 retries, got %d", f.calls["UserResource"])
	}
}
