package app

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Azd325/axeos-axi/internal/ws/wstest"
)

type parityMiner struct {
	scoreboard string
	logs       string
	mu         sync.Mutex
	writes     []string
}

func newParityMiner(t *testing.T, m *parityMiner) string {
	t.Helper()
	files := map[string]string{
		"/api/system/info":              "info.json",
		"/api/system/asic":              "asic.json",
		"/api/system/statistics":        "statistics.json",
		"/api/system/firmware/checksum": "firmware_checksum.json",
		"/api/system/scoreboard":        "scoreboard.json",
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			m.mu.Lock()
			m.writes = append(m.writes, r.Method+" "+r.URL.Path)
			m.mu.Unlock()
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		switch r.URL.Path {
		case "/api/ws":
			s := wstest.Upgrade(w, r, "")
			defer func() { _ = s.Conn.Close() }()
			s.Text("I (10) example: user example-worker connected\n")
			s.Text("I (20) example: ssid example-wifi mac 02:00:00:00:00:10\n")
			time.Sleep(time.Second)
		case "/api/system/logs":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(m.logs))
		default:
			name, ok := files[r.URL.Path]
			if r.URL.Path == "/api/system/statistics" && r.URL.Query().Has("columns") {
				name = "statistics_columns.json"
			}
			if r.URL.Path == "/api/system/scoreboard" && m.scoreboard != "" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(m.scoreboard))
				return
			}
			if !ok {
				http.NotFound(w, r)
				return
			}
			b, err := os.ReadFile("../axeos/testdata/" + name)
			if err != nil {
				t.Error(err)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(b)
		}
	}))
	t.Cleanup(s.Close)
	return s.URL
}

type parityCase struct {
	name     string
	args     []string
	miner    func(*parityMiner)
	host     bool
	saved    bool
	file     string
	noMiners bool
}

func lines(n int) string {
	var b strings.Builder
	for i := range n {
		b.WriteString("I (" + string(rune('0'+i%10)) + ") example: line\n")
	}
	return b.String()
}

var parityCases = []parityCase{
	{name: "home", host: true},
	{name: "home_fields", args: []string{"--fields", "hashrate,temperature"}, host: true},
	{name: "info", args: []string{"info"}, host: true},
	{name: "info_fields", args: []string{"info", "--fields", "version,uptimeSeconds"}, host: true},
	{name: "info_private_fields", args: []string{"info", "--fields", "hostname,macAddr"}, host: true},
	{name: "asic", args: []string{"asic"}, host: true},
	{name: "stats", args: []string{"stats"}, host: true},
	{name: "stats_fields", args: []string{"stats", "--fields", "sample_count,power_w"}, host: true},
	{name: "stats_samples", args: []string{"stats", "--samples", "3"}, host: true},
	{name: "stats_columns", args: []string{"stats", "--columns", "fanRpm,power"}, host: true},
	{name: "firmware", args: []string{"firmware"}, host: true},
	{name: "scoreboard", args: []string{"scoreboard"}, host: true},
	{name: "scoreboard_fields", args: []string{"scoreboard", "--fields", "rank,nonce"}, host: true},
	{name: "scoreboard_empty", args: []string{"scoreboard"}, host: true, miner: func(m *parityMiner) { m.scoreboard = "[]" }},
	{name: "logs", args: []string{"logs"}, host: true, miner: func(m *parityMiner) { m.logs = lines(26) }},
	{name: "logs_lines", args: []string{"logs", "--lines", "3"}, host: true, miner: func(m *parityMiner) {
		m.logs = lines(2) + "I (30) example: user example-worker ssid example-wifi mac 02:00:00:00:00:10\n"
	}},
	{name: "logs_show_private", args: []string{"logs", "--lines", "3", "--show-private"}, host: true, miner: func(m *parityMiner) {
		m.logs = lines(2) + "I (30) example: user example-worker ssid example-wifi mac 02:00:00:00:00:10\n"
	}},
	{name: "logs_empty", args: []string{"logs"}, host: true},
	{name: "logs_follow", args: []string{"logs", "--follow", "2"}, host: true},
	{name: "logs_follow_show_private", args: []string{"logs", "--follow", "2", "--show-private"}, host: true},
	{name: "restart_preview", args: []string{"restart"}, host: true},
	{name: "tuning_preview", args: []string{"tuning", "--frequency", "550", "--core-voltage", "1150"}, host: true},
	{name: "pool_preview", args: []string{"pool", "--url", "pool.example.org", "--port", "3333", "--user", "example-new"}, host: true},
	{name: "pool_preview_show_user", args: []string{"pool", "--user", "example-new", "--show-user"}, host: true},
	{name: "skill_install", args: []string{"skill", "install", "--path", "TMP"}},
	{name: "help_home", args: []string{"--help"}},
	{name: "help_info", args: []string{"info", "--help"}},
	{name: "help_asic", args: []string{"asic", "--help"}},
	{name: "help_stats", args: []string{"stats", "--help"}},
	{name: "help_firmware", args: []string{"firmware", "--help"}},
	{name: "help_scoreboard", args: []string{"scoreboard", "--help"}},
	{name: "help_logs", args: []string{"logs", "--help"}},
	{name: "help_discover", args: []string{"discover", "--help"}},
	{name: "help_restart", args: []string{"restart", "--help"}},
	{name: "help_tuning", args: []string{"tuning", "--help"}},
	{name: "help_pool", args: []string{"pool", "--help"}},
	{name: "help_skill", args: []string{"skill", "--help"}},
	{name: "help_host", args: []string{"host", "--help"}},
	{name: "host_save", args: []string{"host", "save", "MINER"}},
	{name: "host_show", args: []string{"host", "show"}, saved: true},
	{name: "host_show_empty", args: []string{"host", "show"}},
	{name: "host_forget", args: []string{"host", "forget"}, saved: true},
	{name: "host_forget_empty", args: []string{"host", "forget"}},
	{name: "home_saved_host", saved: true},
	{name: "info_saved_host", args: []string{"info"}, saved: true},
	{name: "logs_follow_saved_host", args: []string{"logs", "--follow", "2"}, saved: true},
	{name: "restart_preview_saved_host", args: []string{"restart"}, saved: true},
	{name: "tuning_preview_saved_host", args: []string{"tuning", "--frequency", "550", "--core-voltage", "1150"}, saved: true},
	{name: "pool_preview_saved_host", args: []string{"pool", "--url", "pool.example.org", "--port", "3333", "--user", "example-new"}, saved: true},
	{name: "err_saved_host_invalid", args: []string{"info"}, file: "192.0.2.10\n"},
	{name: "err_saved_host_invalid_home", file: "192.0.2.10\n"},
	{name: "err_host_save_usage", args: []string{"host", "save"}},
	{name: "err_host_save_flag", args: []string{"host", "save", "--host", "192.0.2.10"}},
	{name: "err_host_save_unreachable", args: []string{"host", "save", "127.0.0.1:1"}},
	{name: "err_host_save_invalid", args: []string{"host", "save", "http://"}},
	{name: "err_unknown_flag", args: []string{"info", "--bogus"}, host: true},
	{name: "err_unknown_flag_without_host", args: []string{"info", "--bogus"}},
	{name: "err_unknown_command", args: []string{"bogus"}},
	{name: "err_missing_value", args: []string{"info", "--fields"}},
	{name: "err_host_required", args: []string{"info"}},
	{name: "err_host_required_home", args: nil},
	{name: "err_invalid_host", args: []string{"info", "--host", "http://"}},
	{name: "err_unknown_field", args: []string{"info", "--fields", "nonsense"}, host: true},
	{name: "err_unreachable", args: []string{"info", "--host", "127.0.0.1:1"}},
	{name: "err_logs_unreachable", args: []string{"logs", "--host", "127.0.0.1:1"}},
	{name: "err_follow_unreachable", args: []string{"logs", "--follow", "2", "--host", "127.0.0.1:1"}},
	{name: "err_write_without_value", args: []string{"tuning"}, host: true},
	{name: "err_tuning_value", args: []string{"tuning", "--frequency", "551"}, host: true},
	{name: "err_skill_usage", args: []string{"skill"}},
	{name: "err_skill_flag", args: []string{"skill", "install", "--host", "x"}},
	{name: "discover", args: []string{"discover"}},
	{name: "discover_fields", args: []string{"discover", "--fields", "instance,port,asic_count,board,asic"}},
	{name: "discover_empty", args: []string{"discover"}, noMiners: true},
	{name: "err_discover_host", args: []string{"discover", "--host", "x"}},
}

var (
	hostPattern    = regexp.MustCompile(`http://127\.0\.0\.1:[0-9]+`)
	secondsPattern = regexp.MustCompile(`seconds_followed: [0-9.]+`)
	binPattern     = regexp.MustCompile(`(?m)^bin: .*$`)
)

func runParity(t *testing.T, tc parityCase, extra ...string) (int, string) {
	t.Helper()
	return runParityIn(t, tc, configHome(t), extra...)
}

func runParityIn(t *testing.T, tc parityCase, hostFile string, extra ...string) (int, string) {
	t.Helper()
	shortFollow(t)
	m := &parityMiner{logs: ""}
	if tc.miner != nil {
		tc.miner(m)
	}
	host := newParityMiner(t, m)
	tmp := t.TempDir()
	args := make([]string, 0, len(tc.args)+len(extra)+2)
	for _, arg := range tc.args {
		args = append(args, strings.ReplaceAll(strings.ReplaceAll(arg, "TMP", tmp), "MINER", host))
	}
	if tc.saved {
		saveFile(t, hostFile, host+"\n")
	}
	if tc.file != "" {
		saveFile(t, hostFile, tc.file)
	}
	args = append(args, extra...)
	env := ""
	if tc.host {
		env = host
	}
	a := New(func(string) string { return env })
	a.Browser = &fakeBrowser{services: advertisedMiners()}
	if tc.noMiners {
		a.Browser = &fakeBrowser{}
	}
	var out bytes.Buffer
	code := a.Run(context.Background(), args, &out)
	if len(m.writes) != 0 {
		t.Fatalf("write requests: %v", m.writes)
	}
	text := hostPattern.ReplaceAllString(out.String(), "HOST")
	text = strings.ReplaceAll(text, tmp, "TMP")
	text = strings.ReplaceAll(text, tildePath(hostFile), "HOSTFILE")
	text = secondsPattern.ReplaceAllString(text, "seconds_followed: S")
	text = binPattern.ReplaceAllString(text, "bin: BIN")
	return code, text
}

func TestTOONOutputIsUnchanged(t *testing.T) {
	for _, tc := range parityCases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := runParity(t, tc)
			got := "exit: " + string(rune('0'+code)) + "\n" + out
			path := filepath.Join("testdata", "golden", tc.name+".toon")
			if os.Getenv("UPDATE_GOLDEN") != "" {
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if got != string(want) {
				t.Fatalf("output changed\n%s\nwant\n%s", got, want)
			}
		})
	}
}
