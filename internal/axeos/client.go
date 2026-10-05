package axeos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const Timeout = 4 * time.Second

var ErrHost = errors.New("invalid host; use an HTTP or HTTPS address without credentials, path, query or fragment")

type Client struct {
	base string
	http *http.Client
}

func New(host string) (*Client, error) {
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, ErrHost
	}
	return &Client{base: strings.TrimSuffix(u.String(), "/"), http: &http.Client{
		Timeout:       Timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) Get(ctx context.Context, endpoint string) (map[string]any, error) {
	switch endpoint {
	case "info", "asic", "statistics":
	default:
		return nil, errors.New("unsupported read endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/system/"+endpoint, nil)
	if err != nil {
		return nil, errors.New("cannot create miner request")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("miner unreachable or request timed out; check the host and network")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("miner returned HTTP %d for %s", resp.StatusCode, endpoint)
	}
	const maxBytes = 4 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || len(body) > maxBytes {
		return nil, errors.New("cannot read miner response within the 4 MiB limit")
	}
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil || data == nil {
		return nil, errors.New("miner returned invalid JSON; check AxeOS API compatibility")
	}
	return data, nil
}
