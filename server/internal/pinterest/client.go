// Package pinterest discovers a user's public saved pins by walking the same
// JSON resource endpoints pinterest.com's web client uses.
package pinterest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ErrorCode string

const (
	CodeProfileNotFound ErrorCode = "profile_not_found"
	CodePrivateProfile  ErrorCode = "private_profile"
	CodeNoSaves         ErrorCode = "no_saves"
	CodeRateLimited     ErrorCode = "rate_limited"
	CodeUpstream        ErrorCode = "upstream_error"
)

type Error struct {
	Code    ErrorCode
	Message string
}

func (e *Error) Error() string { return e.Message }

// Retryable reports whether trying again later might succeed.
func (e *Error) Retryable() bool { return e.Code == CodeRateLimited || e.Code == CodeUpstream }

func errorCode(err error) ErrorCode {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

type Pin struct {
	ID            string
	Title         string
	Description   string
	Link          string
	ImageURL      string
	Width         int
	Height        int
	DominantColor string
	BoardName     string
}

type Board struct {
	ID       string
	Name     string
	URL      string
	PinCount int
}

type Profile struct {
	Username string
	PinCount int
	Private  bool
}

type Client struct {
	BaseURL    string
	HTTP       *http.Client
	UserAgent  string
	PageSize   int
	MaxRetries int
	// Backoff returns the delay before retry attempt n (1-based).
	Backoff func(n int) time.Duration
}

func NewClient(baseURL string) *Client {
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		HTTP:       &http.Client{Timeout: 20 * time.Second},
		UserAgent:  "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Safari/605.1.15",
		PageSize:   50,
		MaxRetries: 3,
		Backoff:    func(n int) time.Duration { return time.Duration(n*n) * 2 * time.Second },
	}
}

// Discover walks the user's saves feed, falling back to their boards, and
// returns up to limit unique pins in feed order.
func (c *Client) Discover(ctx context.Context, username string, limit int) ([]Pin, error) {
	profile, err := c.Profile(ctx, username)
	if err != nil {
		code := errorCode(err)
		if code == CodeProfileNotFound || code == CodeRateLimited {
			return nil, err
		}
		// Profile metadata is optional; the feeds below give the final answer.
	} else if profile.Private {
		return nil, &Error{CodePrivateProfile, "This Pinterest profile is private. Make it public to import saves."}
	}

	seen := map[string]bool{}
	var pins []Pin
	add := func(batch []Pin) {
		for _, p := range batch {
			if len(pins) >= limit {
				return
			}
			if p.ID == "" || p.ImageURL == "" || seen[p.ID] {
				continue
			}
			seen[p.ID] = true
			pins = append(pins, p)
		}
	}

	saved, savesErr := c.SavedPins(ctx, username, limit)
	add(saved)
	if errorCode(savesErr) == CodeRateLimited && len(pins) == 0 {
		return nil, savesErr
	}

	if len(pins) == 0 {
		boards, err := c.Boards(ctx, username)
		if err != nil {
			if errorCode(err) == CodeRateLimited || errorCode(err) == CodeProfileNotFound {
				return nil, err
			}
			if savesErr != nil {
				return nil, savesErr
			}
			return nil, err
		}
		for _, b := range boards {
			if len(pins) >= limit {
				break
			}
			batch, err := c.BoardPins(ctx, b, limit-len(pins))
			add(batch)
			if errorCode(err) == CodeRateLimited {
				if len(pins) == 0 {
					return nil, err
				}
				break
			}
		}
	}

	if len(pins) == 0 {
		if profile.Private {
			return nil, &Error{CodePrivateProfile, "This Pinterest profile is private."}
		}
		return nil, &Error{CodeNoSaves, "No public saved pins were found on this Pinterest profile."}
	}
	return pins, nil
}

func (c *Client) Profile(ctx context.Context, username string) (Profile, error) {
	var data struct {
		Username         string `json:"username"`
		PinCount         int    `json:"pin_count"`
		IsPrivateProfile bool   `json:"is_private_profile"`
	}
	_, err := c.resource(ctx, "UserResource", "/"+username+"/", map[string]any{
		"username": username, "field_set_key": "profile",
	}, &data)
	if err != nil {
		return Profile{}, err
	}
	if data.Username == "" {
		// An empty profile is ambiguous; let the feeds decide.
		return Profile{}, &Error{CodeUpstream, "Pinterest returned an empty profile."}
	}
	return Profile{Username: data.Username, PinCount: data.PinCount, Private: data.IsPrivateProfile}, nil
}

func (c *Client) SavedPins(ctx context.Context, username string, limit int) ([]Pin, error) {
	return c.paginate(ctx, limit, func(bookmark string) ([]json.RawMessage, string, error) {
		var data []json.RawMessage
		next, err := c.resource(ctx, "UserPinsResource", "/"+username+"/_saved/", map[string]any{
			"username": username, "field_set_key": "grid_item", "page_size": c.PageSize, "bookmarks": bookmarks(bookmark),
		}, &data)
		return data, next, err
	})
}

func (c *Client) Boards(ctx context.Context, username string) ([]Board, error) {
	var boards []Board
	bookmark := ""
	for page := 0; page < 20; page++ {
		var data []json.RawMessage
		next, err := c.resource(ctx, "BoardsResource", "/"+username+"/", map[string]any{
			"username": username, "field_set_key": "profile_grid_item", "page_size": c.PageSize,
			"privacy_filter": "all", "sort": "last_pinned_to", "bookmarks": bookmarks(bookmark),
		}, &data)
		if err != nil {
			return boards, err
		}
		for _, item := range data {
			var b struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				URL      string `json:"url"`
				PinCount int    `json:"pin_count"`
				Type     string `json:"type"`
			}
			if json.Unmarshal(item, &b) != nil {
				continue
			}
			if b.ID != "" && b.URL != "" && (b.Type == "" || b.Type == "board") {
				boards = append(boards, Board{ID: b.ID, Name: b.Name, URL: b.URL, PinCount: b.PinCount})
			}
		}
		if next == "" || next == "-end-" || len(data) == 0 {
			break
		}
		bookmark = next
	}
	return boards, nil
}

func (c *Client) BoardPins(ctx context.Context, board Board, limit int) ([]Pin, error) {
	pins, err := c.paginate(ctx, limit, func(bookmark string) ([]json.RawMessage, string, error) {
		var data []json.RawMessage
		next, err := c.resource(ctx, "BoardFeedResource", board.URL, map[string]any{
			"board_id": board.ID, "board_url": board.URL, "field_set_key": "react_grid_pin",
			"page_size": c.PageSize, "bookmarks": bookmarks(bookmark),
		}, &data)
		return data, next, err
	})
	for i := range pins {
		if pins[i].BoardName == "" {
			pins[i].BoardName = board.Name
		}
		if pins[i].Title == "" {
			pins[i].Title = board.Name
		}
	}
	return pins, err
}

func (c *Client) paginate(ctx context.Context, limit int, fetch func(bookmark string) ([]json.RawMessage, string, error)) ([]Pin, error) {
	var pins []Pin
	bookmark := ""
	for page := 0; len(pins) < limit && page < 100; page++ {
		raw, next, err := fetch(bookmark)
		if err != nil {
			return pins, err
		}
		for _, item := range raw {
			// Feeds mix pins with stories and modules of other shapes; skip those.
			var r rawPin
			if json.Unmarshal(item, &r) != nil {
				continue
			}
			if p, ok := r.toPin(); ok {
				pins = append(pins, p)
			}
		}
		if next == "" || next == "-end-" || len(raw) == 0 {
			break
		}
		bookmark = next
	}
	if len(pins) > limit {
		pins = pins[:limit]
	}
	return pins, nil
}

func bookmarks(b string) []string {
	if b == "" {
		return []string{}
	}
	return []string{b}
}

type rawImage struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

type rawPin struct {
	ID            string              `json:"id"`
	Type          string              `json:"type"`
	Title         string              `json:"title"`
	GridTitle     string              `json:"grid_title"`
	Description   string              `json:"description"`
	Link          string              `json:"link"`
	DominantColor string              `json:"dominant_color"`
	Images        map[string]rawImage `json:"images"`
	Board         *struct {
		Name string `json:"name"`
	} `json:"board"`
}

func (r rawPin) toPin() (Pin, bool) {
	if r.Type != "" && r.Type != "pin" {
		return Pin{}, false
	}
	img, ok := r.Images["orig"]
	if !ok || img.URL == "" {
		// Fall back to the widest rendition available.
		keys := make([]string, 0, len(r.Images))
		for k := range r.Images {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if cand := r.Images[k]; cand.URL != "" && cand.Width > img.Width {
				img = cand
			}
		}
	}
	if r.ID == "" || img.URL == "" {
		return Pin{}, false
	}
	p := Pin{
		ID: r.ID, Description: strings.TrimSpace(r.Description), Link: r.Link, ImageURL: img.URL,
		Width: img.Width, Height: img.Height, DominantColor: r.DominantColor,
	}
	if r.Board != nil {
		p.BoardName = r.Board.Name
	}
	p.Title = firstNonEmpty(r.Title, r.GridTitle, p.BoardName)
	return p, true
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

type resourceEnvelope struct {
	ResourceResponse struct {
		Status   string          `json:"status"`
		Code     int             `json:"code"`
		Message  string          `json:"message"`
		Data     json.RawMessage `json:"data"`
		Bookmark string          `json:"bookmark"`
		Error    *struct {
			Status  int    `json:"status"`
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	} `json:"resource_response"`
}

// resource calls a Pinterest resource endpoint with retries on rate limits
// and transient failures, decoding resource_response.data into out.
func (c *Client) resource(ctx context.Context, name, sourceURL string, options map[string]any, out any) (string, error) {
	payload, _ := json.Marshal(map[string]any{"options": options, "context": map[string]any{}})
	q := url.Values{}
	q.Set("source_url", sourceURL)
	q.Set("data", string(payload))
	q.Set("_", strconv.FormatInt(time.Now().UnixMilli(), 10))
	endpoint := fmt.Sprintf("%s/resource/%s/get/?%s", c.BaseURL, name, q.Encode())

	var lastErr error
	for attempt := 0; attempt <= c.MaxRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(c.Backoff(attempt)):
			}
		}
		next, err := c.fetch(ctx, endpoint, sourceURL, out)
		if err == nil {
			return next, nil
		}
		lastErr = err
		var pe *Error
		if !errors.As(err, &pe) || !pe.Retryable() {
			return "", err
		}
	}
	return "", lastErr
}

func (c *Client) fetch(ctx context.Context, endpoint, sourceURL string, out any) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json, text/javascript, */*; q=0.01")
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("X-Pinterest-AppState", "active")
	req.Header.Set("X-Pinterest-PWS-Handler", "www"+strings.TrimSuffix(sourceURL, "/")+".js")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", &Error{CodeUpstream, "Pinterest couldn't be reached. Try again shortly."}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return "", &Error{CodeUpstream, "Pinterest returned an incomplete response."}
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		return "", &Error{CodeRateLimited, "Pinterest is rate limiting requests. Try again in a few minutes."}
	case resp.StatusCode == http.StatusNotFound:
		return "", &Error{CodeProfileNotFound, "We couldn't find that Pinterest username."}
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		return "", &Error{CodePrivateProfile, "This Pinterest profile isn't publicly visible."}
	case resp.StatusCode >= 500:
		return "", &Error{CodeUpstream, "Pinterest is having trouble right now. Try again shortly."}
	case resp.StatusCode != http.StatusOK:
		return "", &Error{CodeUpstream, fmt.Sprintf("Pinterest returned HTTP %d.", resp.StatusCode)}
	}

	var env resourceEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return "", &Error{CodeUpstream, "Pinterest returned an unexpected response."}
	}
	rr := env.ResourceResponse
	if rr.Error != nil || (rr.Status != "" && rr.Status != "success") {
		status := 0
		if rr.Error != nil {
			status = rr.Error.Status
		}
		switch status {
		case 404:
			return "", &Error{CodeProfileNotFound, "We couldn't find that Pinterest username."}
		case 429:
			return "", &Error{CodeRateLimited, "Pinterest is rate limiting requests. Try again in a few minutes."}
		case 401, 403:
			return "", &Error{CodePrivateProfile, "This Pinterest profile isn't publicly visible."}
		}
		return "", &Error{CodeUpstream, "Pinterest returned an error."}
	}
	if len(rr.Data) > 0 && string(rr.Data) != "null" {
		if err := json.Unmarshal(rr.Data, out); err != nil {
			return "", &Error{CodeUpstream, "Pinterest returned data in an unexpected format."}
		}
	}
	return rr.Bookmark, nil
}
