// Package nettest keeps tests away from real miners.
package nettest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
)

var (
	mu         sync.Mutex
	violations []string
)

// Run installs the guard, runs the tests and fails the package when any test dialed a non-loopback address.
func Run(m *testing.M) int {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = guardedDial
	http.DefaultTransport = transport
	code := m.Run()
	mu.Lock()
	defer mu.Unlock()
	for _, v := range violations {
		fmt.Fprintf(os.Stderr, "nettest: test dialed a non-loopback address: %s\n", v)
	}
	if len(violations) > 0 && code == 0 {
		code = 1
	}
	return code
}

var dialer net.Dialer

func guardedDial(ctx context.Context, network, address string) (net.Conn, error) {
	if !loopback(address) {
		mu.Lock()
		violations = append(violations, network+" "+address)
		mu.Unlock()
		return nil, fmt.Errorf("nettest: refusing to dial non-loopback address %s", address)
	}
	return dialer.DialContext(ctx, network, address)
}

func loopback(address string) bool {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
