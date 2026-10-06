package axeos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	Timeout = 4 * time.Second
	// The firmware sends its 512 KiB log buffer one line per HTTP chunk (GET_system_logs in ESP-Miner main/http_server/http_server.c).
	LogsTimeout = 15 * time.Second
)

var (
	ErrHost         = errors.New("invalid host; use an HTTP or HTTPS address without credentials, path or query")
	ErrNotFound     = errors.New("miner returned HTTP 404")
	ErrRootRedirect = errors.New("miner returned HTTP 302")
	errInvalidJSON  = errors.New("miner returned invalid JSON; check AxeOS API compatibility")
	errNotText      = errors.New("miner returned a response that is not plain text; check AxeOS API compatibility")
)

type Client struct {
	base string
	http *http.Client
	logs *http.Client
}

func httpClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Timeout:       timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func New(host string) (*Client, error) {
	if !strings.Contains(host, "://") {
		host = "http://" + host
	}
	u, err := url.Parse(host)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery {
		return nil, ErrHost
	}
	return &Client{base: u.Scheme + "://" + u.Host, http: httpClient(Timeout), logs: httpClient(LogsTimeout)}, nil
}

func (c *Client) Get(ctx context.Context, endpoint string) (map[string]any, error) {
	switch endpoint {
	case "info", "asic", "statistics", "firmware/checksum":
	default:
		return nil, errors.New("unsupported read endpoint")
	}
	var data map[string]any
	if err := c.read(ctx, endpoint, &data); err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errInvalidJSON
	}
	return data, nil
}

func (c *Client) GetList(ctx context.Context, endpoint string) ([]any, error) {
	if endpoint != "scoreboard" {
		return nil, errors.New("unsupported read endpoint")
	}
	var data []any
	if err := c.read(ctx, endpoint, &data); err != nil {
		return nil, err
	}
	if data == nil {
		return nil, errInvalidJSON
	}
	return data, nil
}

func (c *Client) GetText(ctx context.Context, endpoint string) (string, error) {
	if endpoint != "logs" {
		return "", errors.New("unsupported read endpoint")
	}
	body, contentType, err := c.fetch(ctx, c.logs, endpoint)
	if err != nil {
		return "", err
	}
	if mediaType, _, err := mime.ParseMediaType(contentType); err != nil || mediaType != "text/plain" {
		return "", errNotText
	}
	return string(body), nil
}

func (c *Client) read(ctx context.Context, endpoint string, data any) error {
	body, _, err := c.fetch(ctx, c.http, endpoint)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, data); err != nil {
		return errInvalidJSON
	}
	return nil
}

func (c *Client) fetch(ctx context.Context, client *http.Client, endpoint string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/api/system/"+endpoint, nil)
	if err != nil {
		return nil, "", errors.New("cannot create miner request")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", errors.New("miner unreachable or request timed out; check the host and network")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", fmt.Errorf("%w for %s", ErrNotFound, endpoint)
	}
	if resp.StatusCode == http.StatusFound && resp.Header.Get("Location") == "/" {
		return nil, "", fmt.Errorf("%w for %s", ErrRootRedirect, endpoint)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("miner returned HTTP %d for %s", resp.StatusCode, endpoint)
	}
	const maxBytes = 4 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", errors.New("miner response timed out or was interrupted; check the host and network")
	}
	if len(body) > maxBytes {
		return nil, "", errors.New("cannot read miner response within the 4 MiB limit")
	}
	return body, resp.Header.Get("Content-Type"), nil
}
