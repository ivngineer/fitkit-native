package pinterest

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	ErrEmptyHandle   = errors.New("enter your Pinterest username or profile link")
	ErrInvalidHandle = errors.New("that doesn't look like a Pinterest username")
	ErrNotProfileURL = errors.New("that link isn't a Pinterest profile — paste your profile link instead")
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9_]{3,30}$`)

// Paths on pinterest.com that are never a profile.
var reservedPaths = map[string]bool{
	"pin": true, "search": true, "ideas": true, "today": true, "business": true, "settings": true,
	"login": true, "signup": true, "explore": true, "categories": true, "about": true, "_": true,
	"resource": true, "videos": true, "shopping": true, "news_hub": true, "notifications": true,
}

// NormalizeHandle accepts a bare username, "@username" or a profile URL
// (any Pinterest country domain) and returns the lowercase username.
func NormalizeHandle(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", ErrEmptyHandle
	}
	s = strings.TrimPrefix(s, "@")

	if strings.Contains(s, "/") || strings.Contains(strings.ToLower(s), "pinterest.") {
		if !strings.Contains(s, "://") {
			s = "https://" + s
		}
		u, err := url.Parse(s)
		if err != nil || !isPinterestHost(u.Hostname()) {
			return "", ErrNotProfileURL
		}
		segments := strings.FieldsFunc(u.Path, func(r rune) bool { return r == '/' })
		if len(segments) == 0 || reservedPaths[strings.ToLower(segments[0])] {
			return "", ErrNotProfileURL
		}
		s = segments[0]
	}

	if !usernamePattern.MatchString(s) {
		return "", ErrInvalidHandle
	}
	return strings.ToLower(s), nil
}

func isPinterestHost(host string) bool {
	host = strings.ToLower(host)
	labels := strings.Split(host, ".")
	for i, l := range labels {
		if l == "pinterest" && i < len(labels)-1 {
			return true
		}
	}
	return false
}
