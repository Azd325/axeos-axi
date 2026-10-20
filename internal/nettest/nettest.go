// Package nettest keeps tests away from real miners.
package nettest

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"
	"syscall"
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
	if err := check(network, address); err != nil {
		return nil, err
	}
	return dialer.DialContext(ctx, network, address)
}

// Control is a net.Dialer Control function that refuses non-loopback addresses.
func Control(network, address string, _ syscall.RawConn) error {
	return check(network, address)
}

func check(network, address string) error {
	if loopback(address) {
		return nil
	}
	mu.Lock()
	violations = append(violations, network+" "+address)
	mu.Unlock()
	return fmt.Errorf("nettest: refusing to dial non-loopback address %s", address)
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
