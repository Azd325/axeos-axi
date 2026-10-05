package mdns

import (
	"context"
	"errors"
	"net"
	"time"
)

var (
	ErrNoInterface = errors.New("no active multicast-capable IPv4 network interface")
	ErrSend        = errors.New("cannot send an mDNS query on any network interface")
)

const (
	maxPacketSize = 9000
	// RFC 6762 section 5.2: repeat a question at intervals of at least one second.
	requeryInterval = time.Second
)

type Multicast struct{}

// Browse sends only mDNS questions and always waits the full duration,
// because the number of responders is unknown.
func (Multicast) Browse(ctx context.Context, service string, wait time.Duration) ([]Service, error) {
	group := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	conns := listen(group)
	if len(conns) == 0 {
		return nil, ErrNoInterface
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer func() {
		cancel()
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()

	packets := make(chan []byte, 64)
	for _, conn := range conns {
		go func() {
			buf := make([]byte, maxPacketSize)
			for {
				n, _, err := conn.ReadFromUDP(buf)
				if err != nil {
					return
				}
				select {
				case packets <- append([]byte(nil), buf[:n]...):
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	send := func(questions []question) bool {
		msg, sent := query(questions), false
		for _, conn := range conns {
			if _, err := conn.WriteToUDP(msg, group); err == nil {
				sent = true
			}
		}
		return sent
	}

	c := newCollector(service)
	if !send([]question{c.browse()}) {
		return nil, ErrSend
	}
	asked := map[string]bool{}
	ticker := time.NewTicker(requeryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return c.services(), nil
		case packet := <-packets:
			c.add(packet)
			var fresh []question
			for _, q := range c.pending() {
				if !asked[q.key()] {
					asked[q.key()] = true
					fresh = append(fresh, q)
				}
			}
			if len(fresh) > 0 {
				send(fresh)
			}
		case <-ticker.C:
			send(append([]question{c.browse()}, c.pending()...))
		}
	}
}

func listen(group *net.UDPAddr) []*net.UDPConn {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var conns []*net.UDPConn
	for _, ifi := range interfaces {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&(net.FlagLoopback|net.FlagPointToPoint) != 0 || !hasIPv4(ifi) {
			continue
		}
		if conn, err := net.ListenMulticastUDP("udp4", &ifi, group); err == nil {
			conns = append(conns, conn)
		}
	}
	return conns
}

func hasIPv4(ifi net.Interface) bool {
	addrs, err := ifi.Addrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		if ipnet, ok := addr.(*net.IPNet); ok && ipnet.IP.To4() != nil {
			return true
		}
	}
	return false
}
