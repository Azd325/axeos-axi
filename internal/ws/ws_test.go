package ws

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Azd325/axeos-axi/internal/ws/wstest"
)

func read(t *testing.T, frames ...[]byte) (string, error) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		server, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = server.Close() }()
		_, _ = server.Write(bytes.Join(frames, nil))
		_ = server.(*net.TCPConn).CloseWrite()
		_, _ = io.Copy(io.Discard, server)
	}()
	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	c := &Conn{conn: client, br: bufio.NewReader(client)}
	return c.ReadMessage(context.Background(), time.Now().Add(2*time.Second))
}

func TestReadFrames(t *testing.T) {
	for _, tc := range []struct {
		name   string
		frames [][]byte
		want   string
		err    error
	}{
		{"text", [][]byte{wstest.Frame(1, true, []byte("I (1) a\n"))}, "I (1) a\n", nil},
		{"fragmented", [][]byte{wstest.Frame(1, false, []byte("I (1) ")), wstest.Frame(0, false, []byte("a")), wstest.Frame(0, true, []byte("\n"))}, "I (1) a\n", nil},
		{"ping between fragments", [][]byte{wstest.Frame(1, false, []byte("x")), wstest.Frame(9, true, []byte("p")), wstest.Frame(0, true, []byte("y"))}, "xy", nil},
		{"unsolicited pong", [][]byte{wstest.Frame(10, true, nil), wstest.Frame(1, true, []byte("z"))}, "z", nil},
		{"extended length", [][]byte{wstest.Frame(1, true, bytes.Repeat([]byte("a"), 300))}, strings.Repeat("a", 300), nil},
		{"close", [][]byte{wstest.Frame(8, true, []byte{0x03, 0xE8})}, "", ErrClosed},
		{"close without payload", [][]byte{wstest.Frame(8, true, nil)}, "", ErrClosed},
		{"close with 1 byte", [][]byte{wstest.Frame(8, true, []byte{3})}, "", ErrProtocol},
		{"oversize frame", [][]byte{wstest.Frame(1, true, bytes.Repeat([]byte("a"), MaxFramePayload+1))}, "", ErrFrameTooLarge},
		{"oversize message", func() [][]byte {
			var f [][]byte
			for i := 0; i <= MaxMessage/MaxFramePayload; i++ {
				f = append(f, wstest.Frame(map[bool]byte{true: 1, false: 0}[i == 0], false, bytes.Repeat([]byte("a"), MaxFramePayload)))
			}
			return f
		}(), "", ErrFrameTooLarge},
		{"masked server frame", [][]byte{{0x81, 0x81, 1, 2, 3, 4, 'a' ^ 1}}, "", ErrProtocol},
		{"reserved bit", [][]byte{{0xC1, 0x01, 'a'}}, "", ErrProtocol},
		{"binary", [][]byte{wstest.Frame(2, true, []byte("a"))}, "", ErrProtocol},
		{"unknown opcode", [][]byte{wstest.Frame(3, true, nil)}, "", ErrProtocol},
		{"continuation without message", [][]byte{wstest.Frame(0, true, []byte("a"))}, "", ErrProtocol},
		{"text inside fragmented message", [][]byte{wstest.Frame(1, false, []byte("a")), wstest.Frame(1, true, []byte("b"))}, "", ErrProtocol},
		{"fragmented control frame", [][]byte{wstest.Frame(9, false, nil)}, "", ErrProtocol},
		{"long control frame", [][]byte{wstest.Frame(9, true, bytes.Repeat([]byte("a"), 126))}, "", ErrProtocol},
		{"invalid UTF-8", [][]byte{wstest.Frame(1, true, []byte{0xff})}, "", ErrProtocol},
		{"cut frame", [][]byte{wstest.Frame(1, true, []byte("abc"))[:3]}, "", io.ErrUnexpectedEOF},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := read(t, tc.frames...)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("%q %v; want %q %v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestPingGetsMaskedPongAndCloseGetsClose(t *testing.T) {
	got := make(chan [2]any, 2)
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		defer func() { _ = s.Conn.Close() }()
		s.Send(9, true, []byte("hi"))
		op, payload, _ := s.ReadFrame()
		got <- [2]any{op, string(payload)}
		s.Send(8, true, []byte{0x03, 0xE8})
		op, payload, _ = s.ReadFrame()
		got <- [2]any{op, payload}
		close(done)
	}))
	defer srv.Close()
	c, err := Dial(context.Background(), srv.URL, "/api/ws")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.ReadMessage(context.Background(), time.Now().Add(2*time.Second)); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
	<-done
	if first := <-got; first != [2]any{byte(10), "hi"} {
		t.Fatalf("%v", first)
	}
	if second := <-got; second[0] != byte(8) || !bytes.Equal(second[1].([]byte), []byte{0x03, 0xE8}) {
		t.Fatalf("%v", second)
	}
}

func TestDialHandshake(t *testing.T) {
	var path, upgrade, version string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, upgrade, version = r.URL.Path, r.Header.Get("Upgrade"), r.Header.Get("Sec-WebSocket-Version")
		s := wstest.Upgrade(w, r, "")
		s.Text("hello\n")
		_ = s.Conn.Close()
	}))
	defer srv.Close()
	c, err := Dial(context.Background(), srv.URL, "/api/ws")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	text, err := c.ReadMessage(context.Background(), time.Now().Add(2*time.Second))
	if err != nil || text != "hello\n" || path != "/api/ws" || upgrade != "websocket" || version != "13" {
		t.Fatalf("%q %v %q %q %q", text, err, path, upgrade, version)
	}
}

func TestDialRefusals(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
		status  int
		err     error
	}{
		{"429", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "full", http.StatusTooManyRequests) }, http.StatusTooManyRequests, nil},
		{"401", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", http.StatusUnauthorized) }, http.StatusUnauthorized, nil},
		{"redirect is not followed", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/", http.StatusFound) }, http.StatusFound, nil},
		{"wrong accept", func(w http.ResponseWriter, r *http.Request) { _ = wstest.Upgrade(w, r, "AAAA") }, 0, ErrHandshake},
		{"extension not asked for", func(w http.ResponseWriter, r *http.Request) {
			conn, rw, _ := w.(http.Hijacker).Hijack()
			_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Extensions: permessage-deflate\r\nSec-WebSocket-Accept: " + wstest.Accept(r.Header.Get("Sec-WebSocket-Key")) + "\r\n\r\n")
			_ = rw.Flush()
			_ = conn.Close()
		}, 0, ErrHandshake},
		{"no upgrade header", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("plain")) }, 200, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var hits int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits++
				if r.URL.Path != "/api/ws" {
					t.Errorf("followed to %s", r.URL.Path)
				}
				tc.handler(w, r)
			}))
			defer srv.Close()
			_, err := Dial(context.Background(), srv.URL, "/api/ws")
			var status *StatusError
			switch {
			case tc.err != nil && !errors.Is(err, tc.err):
				t.Fatal(err)
			case tc.status != 0 && (!errors.As(err, &status) || status.Code != tc.status):
				t.Fatal(err)
			case tc.err == nil && tc.status == 0:
				t.Fatal("no error")
			case hits != 1:
				t.Fatal(hits)
			}
		})
	}
}

func TestDeadlineAndCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := wstest.Upgrade(w, r, "")
		time.Sleep(2 * time.Second)
		_ = s.Conn.Close()
	}))
	defer srv.Close()
	c, err := Dial(context.Background(), srv.URL, "/api/ws")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	if _, err := c.ReadMessage(context.Background(), time.Now().Add(50*time.Millisecond)); !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := c.ReadMessage(ctx, time.Now().Add(time.Second)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
