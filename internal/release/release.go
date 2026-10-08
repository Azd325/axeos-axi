package release

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"time"
)

const (
	// Latest is the one address that leaves the local network. GitHub marks one non-draft, non-pre-release
	// release as latest (GET /repos/{owner}/{repo}/releases/latest in the GitHub REST API).
	Latest  = "https://api.github.com/repos/bitaxeorg/ESP-Miner/releases/latest"
	Timeout = 10 * time.Second

	maxBytes = 1 << 20
)

var (
	ErrUnreachable = errors.New("GitHub is unreachable or the request timed out")
	ErrRateLimit   = errors.New("GitHub refused the request with HTTP 403 or 429; the rate limit for requests without a token is reached")
	ErrNoTag       = errors.New("the GitHub answer has no release tag")
	ErrInvalid     = errors.New("the GitHub answer is not a release document")
)

type Release struct {
	Tag, Name, Date, URL string
}

type Client struct {
	url, userAgent string
	http           *http.Client
}

// New returns a client for the fixed release address, or for url when it is not empty.
// The request carries no token and no cookie, and its User-Agent is the tool name and version only.
func New(url, version string) *Client {
	if url == "" {
		url = Latest
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	return &Client{
		url:       url,
		userAgent: "axeos-axi/" + version,
		http: &http.Client{
			Transport:     transport,
			Timeout:       Timeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (c *Client) Latest(ctx context.Context) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return Release{}, ErrUnreachable
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Release{}, ErrUnreachable
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return Release{}, ErrRateLimit
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return Release{}, fmt.Errorf("GitHub answered with HTTP %d; the tool follows no redirect", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return Release{}, fmt.Errorf("GitHub answered with HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || len(body) > maxBytes {
		return Release{}, ErrInvalid
	}
	var doc struct {
		Tag  string `json:"tag_name"`
		Name string `json:"name"`
		Date string `json:"published_at"`
		URL  string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return Release{}, ErrInvalid
	}
	if doc.Tag == "" {
		return Release{}, ErrNoTag
	}
	return Release{Tag: doc.Tag, Name: doc.Name, Date: doc.Date, URL: doc.URL}, nil
}

var stable = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

const (
	UpToDate          = "up_to_date"
	UpdateAvailable   = "update_available"
	NewerThanRelease  = "newer_than_release"
	Unknown           = "unknown"
	FormReason        = "is not in the form vMAJOR.MINOR.PATCH"
	minerVersionLabel = "the miner version "
	releaseTagLabel   = "the release tag "
)

// Compare returns the comparison result, and for Unknown a short reason.
func Compare(miner, release string) (string, string) {
	m, ok := parse(miner)
	if !ok {
		return Unknown, minerVersionLabel + strconv.Quote(miner) + " " + FormReason
	}
	r, ok := parse(release)
	if !ok {
		return Unknown, releaseTagLabel + strconv.Quote(release) + " " + FormReason
	}
	for i := range m {
		switch {
		case m[i] < r[i]:
			return UpdateAvailable, ""
		case m[i] > r[i]:
			return NewerThanRelease, ""
		}
	}
	return UpToDate, ""
}

func parse(s string) ([3]uint64, bool) {
	var out [3]uint64
	match := stable.FindStringSubmatch(s)
	if match == nil {
		return out, false
	}
	for i := range out {
		n, err := strconv.ParseUint(match[i+1], 10, 64)
		if err != nil {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
