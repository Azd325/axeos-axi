package release

import "testing"

func TestDefaultAddressIsFixed(t *testing.T) {
	if Latest != "https://api.github.com/repos/bitaxeorg/ESP-Miner/releases/latest" {
		t.Fatalf("release address changed: %s", Latest)
	}
	if got := New("", "1.0.0").url; got != Latest {
		t.Fatalf("default client address %s", got)
	}
}

func TestCompare(t *testing.T) {
	for _, tc := range []struct{ miner, tag, want string }{
		{"v2.15.3", "v2.15.3", UpToDate},
		{"v2.15.2", "v2.15.3", UpdateAvailable},
		{"v2.9.9", "v2.10.0", UpdateAvailable},
		{"v1.99.99", "v2.0.0", UpdateAvailable},
		{"v2.16.0", "v2.15.3", NewerThanRelease},
		{"v2.10.0", "v2.9.9", NewerThanRelease},
		{"v3.0.0", "v2.99.99", NewerThanRelease},
		{"v2.15.2rc0", "v2.15.3", Unknown},
		{"v2.15.2rc0-30-gabc1234", "v2.15.3", Unknown},
		{"v2.15.3-dirty", "v2.15.3", Unknown},
		{"Unknown", "v2.15.3", Unknown},
		{"", "v2.15.3", Unknown},
		{"2.15.3", "v2.15.3", Unknown},
		{"v02.15.3", "v2.15.3", Unknown},
		{"v2.15", "v2.15.3", Unknown},
		{"v2.15.3", "v2.15.4-beta", Unknown},
		{"v2.15.3", "latest", Unknown},
		{"v99999999999999999999.0.0", "v2.15.3", Unknown},
	} {
		got, reason := Compare(tc.miner, tc.tag)
		if got != tc.want || (got == Unknown) != (reason != "") {
			t.Errorf("Compare(%q, %q) = %s, %q; want %s", tc.miner, tc.tag, got, reason, tc.want)
		}
	}
}
