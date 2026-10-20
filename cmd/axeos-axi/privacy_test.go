package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	ipv4Token     = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)
	ipv6Token     = regexp.MustCompile(`(?i)[0-9a-f:]*:[0-9a-f:]*:[0-9a-f:]*`)
	macToken      = regexp.MustCompile(`(?i)\b(?:[0-9a-f]{2}:){5}[0-9a-f]{2}\b`)
	localToken    = regexp.MustCompile(`(?i)[a-z0-9_.-]*\.local\b`)
	bitcoinToken  = regexp.MustCompile(`\b(?:bc1|[13])[a-zA-Z0-9]{25,39}\b`)
	privateAssign = regexp.MustCompile(`(?i)(?:ssid|stratum.?user)"?\s*[:= ]\s*"?([^\s",}]+)`)

	exampleMAC   = regexp.MustCompile(`^02:00:00:00:00:[0-9a-f]{2}$`)
	exampleValue = regexp.MustCompile(`^(?:example-[a-z0-9-]+|<[a-z-]+>|null)$`)

	documentationNets = parseCIDRs("192.0.2.0/24", "198.51.100.0/24", "203.0.113.0/24", "2001:db8::/32", "127.0.0.0/8", "::1/128")

	exampleHostnames = map[string]bool{
		"bitaxe-example.local":    true,
		"bitaxe-other.local":      true,
		"bitaxe-unresolved.local": true,
	}
)

func parseCIDRs(cidrs ...string) []*net.IPNet {
	var nets []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		nets = append(nets, n)
	}
	return nets
}

func documentationIP(ip net.IP) bool {
	for _, n := range documentationNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

func allowedLocal(host string) bool {
	return host == ".local" || strings.HasSuffix(host, "._tcp.local") || strings.HasPrefix(host, "example-") || exampleHostnames[strings.ToLower(host)]
}

func trimSingleColons(tok string) string {
	if !strings.HasPrefix(tok, "::") {
		tok = strings.TrimPrefix(tok, ":")
	}
	if !strings.HasSuffix(tok, "::") {
		tok = strings.TrimSuffix(tok, ":")
	}
	return tok
}

func privacyViolations(text string) []string {
	var found []string
	for _, tok := range ipv4Token.FindAllString(text, -1) {
		if ip := net.ParseIP(tok); ip != nil && !documentationIP(ip) {
			found = append(found, "IPv4 address "+tok)
		}
	}
	for _, tok := range ipv6Token.FindAllString(text, -1) {
		tok = trimSingleColons(tok)
		if ip := net.ParseIP(tok); ip != nil && !documentationIP(ip) {
			found = append(found, "IPv6 address "+tok)
		}
	}
	for _, tok := range macToken.FindAllString(text, -1) {
		if !exampleMAC.MatchString(strings.ToLower(tok)) {
			found = append(found, "MAC address "+tok)
		}
	}
	for _, tok := range localToken.FindAllString(text, -1) {
		if !allowedLocal(tok) {
			found = append(found, "hostname "+tok)
		}
	}
	for _, tok := range bitcoinToken.FindAllString(text, -1) {
		found = append(found, "Bitcoin address "+tok)
	}
	for _, m := range privateAssign.FindAllStringSubmatch(text, -1) {
		if !exampleValue.MatchString(m[1]) {
			found = append(found, "value after "+strings.TrimSpace(m[0][:strings.IndexAny(m[0], `":= `)])+" "+m[1])
		}
	}
	return found
}

func scanFiles(root string, rels []string) []string {
	var found []string
	for _, rel := range rels {
		data, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			found = append(found, rel+": "+err.Error())
			continue
		}
		for lineNo, line := range strings.Split(string(data), "\n") {
			for _, v := range privacyViolations(line) {
				found = append(found, fmt.Sprintf("%s:%d: %s is not a documentation or example value", rel, lineNo+1, v))
			}
		}
	}
	return found
}

func TestFixturesAndDocsCarryOnlyDocumentationValues(t *testing.T) {
	root := filepath.Join("..", "..")
	tracked, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Fatal(err)
	}
	var rels []string
	for _, rel := range strings.Split(string(tracked), "\x00") {
		if rel != "" && (strings.HasSuffix(rel, ".md") || strings.Contains("/"+rel, "/testdata/")) {
			rels = append(rels, rel)
		}
	}
	if len(rels) == 0 {
		t.Fatal("no fixture or documentation file found; the walk root is wrong")
	}
	for _, v := range scanFiles(root, rels) {
		t.Error(v)
	}
}

func TestScanFilesReportsBarePrivateAddresses(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"clean.md":  "run axeos-axi --host 192.0.2.10 and 2001:db8::1\n",
		"ipv4.md":   "intro\nrun axeos-axi --host 192.168.1.20\n",
		"ipv6.json": `{"addr": "2a02:8071::1"}` + "\n",
		"full.json": `{"ipv6": "fe80:0000:0000:0000:a6cf:12ff:fe34:5678"}` + "\n",
	}
	var rels []string
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		rels = append(rels, name)
	}
	got := strings.Join(scanFiles(dir, rels), "\n")
	for _, want := range []string{"ipv4.md:2: IPv4 address 192.168.1.20", "ipv6.json:1: IPv6 address 2a02:8071::1", "full.json:1: IPv6 address fe80:0000:0000:0000:a6cf:12ff:fe34:5678"} {
		if !strings.Contains(got, want) {
			t.Errorf("scan result %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "clean.md") {
		t.Errorf("scan flagged documentation addresses: %q", got)
	}
}

func TestPrivacyViolations(t *testing.T) {
	for name, tc := range map[string]struct {
		line string
		want int
	}{
		"documentation IPv4":    {"http://192.0.2.10/api", 0},
		"loopback":              {"127.0.0.1:8080 and [::1]", 0},
		"documentation IPv6":    {"2001:db8::10", 0},
		"example MAC":           {"mac 02:00:00:00:00:10", 0},
		"example names":         {`"ssid": "example-wifi", "stratumUser": "example-worker"`, 0},
		"placeholders":          {"user <pool-user> ssid <wifi-name>", 0},
		"service type":          {"_axeos._sub._http._tcp.local", 0},
		"example host":          {"example-miner.local", 0},
		"field name list":       {"stratumUser,fallbackStratumUser,ssid,macAddr", 0},
		"real IPv4":             {"http://203.0.114.7/api", 1},
		"private IPv4":          {"192.168.1.20", 1},
		"real IPv6":             {"2a02:8071::1", 1},
		"real MAC":              {"mac a4:cf:12:34:56:78", 1},
		"real hostname":         {"bitaxe-7a3f.local", 1},
		"real SSID":             {`"ssid": "HomeNet"`, 1},
		"real pool user":        {`"stratumUser": "bc1qar0srrr7xfkvy5l643lydnw9re59gtzzwf5mdq.rig1"`, 2},
		"Bitcoin address":       {"pay 1BvBMSEYstWetqTFn5Au4m4GFg7xJaNVN2", 1},
		"uppercase real MAC":    {"A4:CF:12:34:56:78", 1},
		"real worker in log":    {"I (30) example: user x ssid HomeNet mac 02:00:00:00:00:10", 1},
		"time stamp is no IPv6": {"at 11:33:52", 0},
	} {
		t.Run(name, func(t *testing.T) {
			if got := privacyViolations(tc.line); len(got) != tc.want {
				t.Errorf("privacyViolations(%q) = %v, want %d finding(s)", tc.line, got, tc.want)
			}
		})
	}
}
