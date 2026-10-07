package ws

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// The firmware sends at most 4096 bytes in a frame (WS_LOG_CHUNK_SIZE in ESP-Miner main/http_server/websocket_log.c).
	MaxFramePayload = 16 << 10
	MaxMessage      = 64 << 10

	handshakeTimeout = 4 * time.Second
	writeTimeout     = 4 * time.Second
	acceptGUID       = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
)

const (
	opContinuation = 0x0
	opText         = 0x1
	opBinary       = 0x2
	opClose        = 0x8
	opPing         = 0x9
	opPong         = 0xA
)

var (
	ErrClosed        = errors.New("the server closed the connection")
	ErrFrameTooLarge = errors.New("a frame is larger than the limit")
	ErrProtocol      = errors.New("the server broke the WebSocket protocol")
	ErrHandshake     = errors.New("the server did not accept the WebSocket handshake")
)

type StatusError struct {
	Code     int
	Location string
}

func (e *StatusError) Error() string { return fmt.Sprintf("the server answered HTTP %d", e.Code) }

type Conn struct {
	conn net.Conn
	br   *bufio.Reader
}

// Dial opens a read-only WebSocket to base+path. It follows no redirect and sends no data frame, ever.
func Dial(ctx context.Context, base, path string) (*Conn, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return nil, errors.New("invalid address")
	}
	address := u.Host
	if u.Port() == "" {
		if u.Scheme == "https" {
			address = net.JoinHostPort(u.Hostname(), "443")
		} else {
			address = net.JoinHostPort(u.Hostname(), "80")
		}
	}
	dialer := &net.Dialer{Timeout: handshakeTimeout}
	var conn net.Conn
	if u.Scheme == "https" {
		conn, err = (&tls.Dialer{NetDialer: dialer, Config: &tls.Config{ServerName: u.Hostname()}}).DialContext(ctx, "tcp", address)
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot connect: %w", err)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	_ = conn.SetDeadline(time.Now().Add(handshakeTimeout))

	keyBytes := make([]byte, 16)
	if _, err := rand.Read(keyBytes); err != nil {
		_ = conn.Close()
		return nil, err
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	request := "GET " + path + " HTTP/1.1\r\nHost: " + u.Host + "\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\n\r\n"
	if _, err := io.WriteString(conn, request); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("cannot send the handshake: %w", err)
	}
	br := bufio.NewReader(conn)
	req, _ := http.NewRequest(http.MethodGet, base+path, nil)
	resp, err := http.ReadResponse(br, req)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("%w: no valid HTTP answer", ErrHandshake)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		_ = conn.Close()
		return nil, &StatusError{Code: resp.StatusCode, Location: resp.Header.Get("Location")}
	}
	sum := sha1.Sum([]byte(key + acceptGUID))
	switch {
	case !strings.EqualFold(resp.Header.Get("Upgrade"), "websocket"):
		err = fmt.Errorf("%w: no Upgrade: websocket header", ErrHandshake)
	case !hasToken(resp.Header.Values("Connection"), "upgrade"):
		err = fmt.Errorf("%w: no Connection: Upgrade header", ErrHandshake)
	case resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(sum[:]):
		err = fmt.Errorf("%w: wrong Sec-WebSocket-Accept key", ErrHandshake)
	case resp.Header.Get("Sec-WebSocket-Extensions") != "" || resp.Header.Get("Sec-WebSocket-Protocol") != "":
		err = fmt.Errorf("%w: an extension or protocol that was not requested", ErrHandshake)
	}
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return &Conn{conn: conn, br: br}, nil
}

func hasToken(values []string, token string) bool {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// ReadMessage returns the next text message. It answers a ping with a pong and a close frame with a close frame.
// A deadline or a cancelled ctx ends the read with os.ErrDeadlineExceeded or the error of ctx.
func (c *Conn) ReadMessage(ctx context.Context, deadline time.Time) (string, error) {
	_ = c.conn.SetReadDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = c.conn.SetReadDeadline(time.Unix(1, 0)) })
	defer stop()
	text, err := c.readMessage()
	if err != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	return text, err
}

func (c *Conn) readMessage() (string, error) {
	var message []byte
	inMessage := false
	for {
		op, final, payload, err := c.readFrame()
		if err != nil {
			return "", err
		}
		switch op {
		case opPing:
			if err := c.writeControl(opPong, payload); err != nil {
				return "", err
			}
			continue
		case opPong:
			continue
		case opClose:
			if len(payload) == 1 {
				return "", fmt.Errorf("%w: close frame with a 1 byte payload", ErrProtocol)
			}
			reply := []byte(nil)
			if len(payload) >= 2 {
				reply = payload[:2]
			}
			_ = c.writeControl(opClose, reply)
			return "", ErrClosed
		case opText:
			if inMessage {
				return "", fmt.Errorf("%w: a new message inside a fragmented message", ErrProtocol)
			}
		case opContinuation:
			if !inMessage {
				return "", fmt.Errorf("%w: continuation frame without a message", ErrProtocol)
			}
		case opBinary:
			return "", fmt.Errorf("%w: binary frame", ErrProtocol)
		default:
			return "", fmt.Errorf("%w: unknown opcode %d", ErrProtocol, op)
		}
		inMessage = true
		message = append(message, payload...)
		if len(message) > MaxMessage {
			return "", fmt.Errorf("%w: message of more than %d bytes", ErrFrameTooLarge, MaxMessage)
		}
		if final {
			if !utf8.Valid(message) {
				return "", fmt.Errorf("%w: text that is not UTF-8", ErrProtocol)
			}
			return string(message), nil
		}
	}
}

func (c *Conn) readFrame() (op byte, final bool, payload []byte, err error) {
	var head [2]byte
	if _, err = io.ReadFull(c.br, head[:]); err != nil {
		return 0, false, nil, eof(err)
	}
	final = head[0]&0x80 != 0
	op = head[0] & 0x0F
	if head[0]&0x70 != 0 {
		return 0, false, nil, fmt.Errorf("%w: reserved bits are set", ErrProtocol)
	}
	if head[1]&0x80 != 0 {
		return 0, false, nil, fmt.Errorf("%w: masked frame from a server", ErrProtocol)
	}
	length := uint64(head[1] & 0x7F)
	switch length {
	case 126:
		var ext [2]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return 0, false, nil, eof(err)
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err = io.ReadFull(c.br, ext[:]); err != nil {
			return 0, false, nil, eof(err)
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if op&0x8 != 0 && (!final || length > 125) {
		return 0, false, nil, fmt.Errorf("%w: invalid control frame", ErrProtocol)
	}
	if length > MaxFramePayload {
		return 0, false, nil, fmt.Errorf("%w: %d bytes, limit %d", ErrFrameTooLarge, length, MaxFramePayload)
	}
	payload = make([]byte, length)
	if _, err = io.ReadFull(c.br, payload); err != nil {
		return 0, false, nil, eof(err)
	}
	return op, final, payload, nil
}

func eof(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// writeControl is the only way to write a frame; the client has no function that sends a text or binary data frame.
func (c *Conn) writeControl(op byte, payload []byte) error {
	if len(payload) > 125 {
		return errors.New("control payload too long")
	}
	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	frame := make([]byte, 0, 6+len(payload))
	frame = append(frame, 0x80|op, 0x80|byte(len(payload)))
	frame = append(frame, mask[:]...)
	for i, b := range payload {
		frame = append(frame, b^mask[i%4])
	}
	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := c.conn.Write(frame)
	return err
}

// Close sends a close frame with code 1000, then closes the connection.
func (c *Conn) Close() error {
	_ = c.writeControl(opClose, []byte{0x03, 0xE8})
	return c.conn.Close()
}
