package wstest

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"net"
	"net/http"
)

type Session struct {
	Conn net.Conn
	Br   *bufio.Reader
}

func Accept(key string) string {
	sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	return base64.StdEncoding.EncodeToString(sum[:])
}

func Frame(op byte, final bool, payload []byte) []byte {
	first := op
	if final {
		first |= 0x80
	}
	frame := []byte{first}
	switch n := len(payload); {
	case n < 126:
		frame = append(frame, byte(n))
	case n < 1<<16:
		frame = append(frame, 126, byte(n>>8), byte(n))
	default:
		frame = binary.BigEndian.AppendUint64(append(frame, 127), uint64(n))
	}
	return append(frame, payload...)
}

func (s *Session) Send(op byte, final bool, payload []byte) {
	_, _ = s.Conn.Write(Frame(op, final, payload))
}

func (s *Session) Text(text string) { s.Send(1, true, []byte(text)) }

// Upgrade answers the handshake with the given accept value (empty: the right one) and hands over the connection.
func Upgrade(w http.ResponseWriter, r *http.Request, accept string) *Session {
	if accept == "" {
		accept = Accept(r.Header.Get("Sec-WebSocket-Key"))
	}
	conn, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return nil
	}
	_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + "\r\n\r\n")
	_ = rw.Flush()
	return &Session{Conn: conn, Br: rw.Reader}
}

// ReadFrame reads one masked client frame and returns its opcode and unmasked payload.
func (s *Session) ReadFrame() (byte, []byte, error) {
	var head [2]byte
	if _, err := s.Br.Read(head[:1]); err != nil {
		return 0, nil, err
	}
	if _, err := s.Br.Read(head[1:]); err != nil {
		return 0, nil, err
	}
	n := int(head[1] & 0x7F)
	var mask [4]byte
	for i := range mask {
		b, err := s.Br.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		mask[i] = b
	}
	payload := make([]byte, n)
	for i := range payload {
		b, err := s.Br.ReadByte()
		if err != nil {
			return 0, nil, err
		}
		payload[i] = b ^ mask[i%4]
	}
	return head[0] & 0x0F, payload, nil
}
