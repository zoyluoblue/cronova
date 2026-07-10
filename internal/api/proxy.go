package api

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// SetTrustedProxies configures peers allowed to supply X-Forwarded-* headers.
// Entries are IPs or CIDRs. Loopback peers are always trusted for a same-host
// reverse proxy; direct non-loopback clients are never trusted implicitly.
func (s *Server) SetTrustedProxies(entries []string) error {
	var nets []*net.IPNet
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if ip := net.ParseIP(entry); ip != nil {
			bits := 128
			if ip.To4() != nil {
				ip = ip.To4()
				bits = 32
			}
			nets = append(nets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		_, network, err := net.ParseCIDR(entry)
		if err != nil {
			return fmt.Errorf("invalid trusted proxy %q: use an IP or CIDR", entry)
		}
		nets = append(nets, network)
	}
	s.trustedProxies = nets
	return nil
}

func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	return net.ParseIP(strings.Trim(host, "[]"))
}

func (s *Server) trustedProxy(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	for _, network := range s.trustedProxies {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *Server) clientIP(r *http.Request) string {
	peer := remoteIP(r)
	if !s.trustedProxy(peer) {
		return clientIP(r.RemoteAddr)
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := net.ParseIP(strings.TrimSpace(parts[i]))
		if candidate == nil {
			continue
		}
		if !s.trustedProxy(candidate) {
			return candidate.String()
		}
	}
	if peer != nil {
		return peer.String()
	}
	return clientIP(r.RemoteAddr)
}

func (s *Server) effectiveHost(r *http.Request) string {
	if s.trustedProxy(remoteIP(r)) {
		if xfh := r.Header.Get("X-Forwarded-Host"); xfh != "" {
			if i := strings.IndexByte(xfh, ','); i >= 0 {
				xfh = xfh[:i]
			}
			return strings.TrimSpace(xfh)
		}
	}
	return r.Host
}
