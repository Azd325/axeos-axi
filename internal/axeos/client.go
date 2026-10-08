package axeos

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Azd325/axeos-axi/internal/ws"
)

const (
	Timeout = 4 * time.Second
	// The firmware sends its 512 KiB log buffer one line per HTTP chunk (GET_system_logs in ESP-Miner main/http_server/http_server.c).
	LogsTimeout = 15 * time.Second
	// The firmware reports each pool password as this value (system_api_get_full_json in ESP-Miner main/http_server/system_api_json.c).
	KeepPassword = "*****"
)

var (
	ErrHost         = errors.New("invalid host; use an HTTP or HTTPS address without credentials, path or query")
	ErrNotFound     = errors.New("miner returned HTTP 404")
	ErrRootRedirect = errors.New("miner returned HTTP 302")
	ErrNotSent      = errors.New("the request was not sent")
	ErrNoAnswer     = errors.New("the request was sent, but the miner closed the connection or did not answer in time")
	ErrStreamFull   = errors.New("the miner has no free WebSocket place; it holds at most 10")
	ErrAccess       = errors.New("the miner refused access to the log stream")
	ErrHandshake    = errors.New("the miner did not accept the WebSocket handshake")
	errInvalidJSON  = errors.New("miner returned invalid JSON; check AxeOS API compatibility")
	errNotText      = errors.New("miner returned a response that is not plain text; check AxeOS API compatibility")
)

type Client struct {
	base string
	http *http.Client
	logs *http.Client
}

func httpClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	return &http.Client{
		Transport:     transport,
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
	host = strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if port := u.Port(); port != "" && port != map[string]string{"http": "80", "https": "443"}[u.Scheme] {
		host += ":" + port
	}
	return &Client{base: u.Scheme + "://" + host, http: httpClient(Timeout), logs: httpClient(LogsTimeout)}, nil
}

func (c *Client) Base() string { return c.base }

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

// StatisticsColumns are the names that GET /api/system/statistics accepts in its columns query, in the order of the answer
// (strToDataSource and GET_system_statistics in ESP-Miner main/http_server/http_server.c, tags v2.15.3 and master).
// The firmware ignores any other name, and it answers every column when no name matches.
var StatisticsColumns = []string{
	"hashrate", "hashrate_1m", "hashrate_10m", "hashrate_1h", "errorPercentage", "asicTemp", "asicTemp2", "vrTemp", "asicVoltage",
	"voltage", "power", "current", "fanSpeed", "fanRpm", "fan2Rpm", "wifiRssi", "freeHeap", "responseTime",
}

// Firmware older than v2.11.0 has no columns query and answers its own older column names (v2.10.1 create_json_statistics_all).
func (c *Client) GetStatistics(ctx context.Context, columns []string) (map[string]any, error) {
	for _, name := range columns {
		if !slices.Contains(StatisticsColumns, name) {
			return nil, errors.New("unsupported statistics column")
		}
	}
	endpoint := "statistics?columns=" + strings.Join(columns, ",")
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

// The firmware answers HTTP 200 before it restarts (POST_restart in ESP-Miner main/http_server/http_server.c):
// JSON from v2.13.0, plain text before, so the body is not read.
func (c *Client) Post(ctx context.Context, endpoint string) error {
	if endpoint != "restart" {
		return errors.New("unsupported write endpoint")
	}
	return c.send(ctx, http.MethodPost, "/"+endpoint, nil, endpoint)
}

// The firmware writes only the keys of the body and answers HTTP 200 with an empty body
// (PATCH_update_settings in ESP-Miner main/http_server/http_server.c).
func (c *Client) Patch(ctx context.Context, settings map[string]int) error {
	if len(settings) == 0 {
		return errors.New("unsupported write setting")
	}
	for name := range settings {
		if name != "frequency" && name != "coreVoltage" {
			return errors.New("unsupported write setting")
		}
	}
	body, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("cannot create miner request; %w", ErrNotSent)
	}
	return c.send(ctx, http.MethodPatch, "", body, "settings")
}

// The firmware replaces the whole record of each pool in the list, checks each record before it stores one,
// and keeps the stored password when a record carries KeepPassword
// (check_settings_and_update and update_pool_nvs in ESP-Miner main/http_server/http_server.c).
func (c *Client) PatchPools(ctx context.Context, pools []map[string]any) error {
	if len(pools) == 0 {
		return errors.New("unsupported write setting")
	}
	for _, pool := range pools {
		if _, ok := pool["id"].(float64); !ok || pool["stratumPassword"] != KeepPassword {
			return errors.New("unsupported write setting")
		}
	}
	body, err := json.Marshal(map[string]any{"pools": pools})
	if err != nil {
		return fmt.Errorf("cannot create miner request; %w", ErrNotSent)
	}
	return c.send(ctx, http.MethodPatch, "", body, "settings")
}

func (c *Client) send(ctx context.Context, method, path string, body []byte, subject string) error {
	// WroteRequest can fire after Do returns, so an established connection counts as sent.
	var sent atomic.Bool
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { sent.Store(true) }}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), method, c.base+"/api/system"+path, reader)
	if err != nil {
		return fmt.Errorf("cannot create miner request; %w", ErrNotSent)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if sent.Load() {
			return ErrNoAnswer
		}
		return fmt.Errorf("miner unreachable or request timed out; %w", ErrNotSent)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("the request was sent; miner returned HTTP %d for %s", resp.StatusCode, subject)
	}
	return nil
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

// OpenLogStream opens the read-only log stream /api/ws. The firmware sends each new log line there and no old line,
// answers HTTP 401 outside the private network, and HTTP 429 when all 10 WebSocket places are taken
// (websocket_pre_handshake and websocket_log_task in ESP-Miner main/http_server/websocket.c and websocket_log.c, tags v2.15.3 and master).
func (c *Client) OpenLogStream(ctx context.Context) (*ws.Conn, error) {
	conn, err := ws.Dial(ctx, c.base, "/api/ws")
	var status *ws.StatusError
	switch {
	case err == nil:
		return conn, nil
	case errors.As(err, &status):
		switch {
		case status.Code == http.StatusNotFound:
			return nil, fmt.Errorf("%w for log stream", ErrNotFound)
		case status.Code == http.StatusFound && status.Location == "/":
			return nil, fmt.Errorf("%w for log stream", ErrRootRedirect)
		case status.Code == http.StatusTooManyRequests:
			return nil, ErrStreamFull
		case status.Code == http.StatusUnauthorized || status.Code == http.StatusForbidden:
			return nil, fmt.Errorf("%w; HTTP %d", ErrAccess, status.Code)
		}
		return nil, fmt.Errorf("%w; HTTP %d", ErrHandshake, status.Code)
	case errors.Is(err, ws.ErrHandshake):
		return nil, fmt.Errorf("%w; %s", ErrHandshake, strings.TrimPrefix(err.Error(), ws.ErrHandshake.Error()+": "))
	}
	return nil, errors.New("miner unreachable or request timed out; check the host and network")
}
