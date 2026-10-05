package web

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
)

func normalizeConnectionURL(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" || strings.Contains(u.Host, "%") {
		return "", connectionError(400, "invalid_connection", "use an HTTPS origin without a path, credentials, query or fragment")
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", connectionError(400, "invalid_connection", "instance URL needs a host")
	}
	port := u.Port()
	if port == "443" {
		port = ""
	}
	if port != "" {
		if _, err := net.LookupPort("tcp", port); err != nil {
			return "", connectionError(400, "invalid_connection", "instance URL has an invalid port")
		}
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return "https://" + host, nil
}

func connectionAddressClass(addr netip.Addr) (private, blocked bool) {
	addr = addr.Unmap()
	if !addr.IsValid() || addr.Zone() != "" || addr.IsUnspecified() || addr.IsMulticast() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return false, true
	}
	// Cloud credential endpoints are never valid UAM targets, even with private access approved.
	if addr == netip.MustParseAddr("100.100.100.200") || addr == netip.MustParseAddr("fd00:ec2::254") {
		return false, true
	}
	private = addr.IsPrivate() || addr.IsLoopback() || netip.MustParsePrefix("100.64.0.0/10").Contains(addr)
	return private, !private && !addr.IsGlobalUnicast()
}

func resolveConnectionAddresses(ctx context.Context, host string) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip.Unmap()}, nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, connectionError(502, "remote_unavailable", "could not resolve the connected instance")
	}
	for i := range ips {
		ips[i] = ips[i].Unmap()
	}
	return ips, nil
}

// Private destinations are explicit and pinned when paired. A later public DNS
// answer cannot silently rebind to another private network or metadata service.
func connectionAddresses(ctx context.Context, base string, allowPrivate bool) ([]string, error) {
	u, _ := url.Parse(base)
	ips, err := resolveConnectionAddresses(ctx, u.Hostname())
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, ip := range ips {
		private, blocked := connectionAddressClass(ip)
		if blocked {
			return nil, connectionError(400, "invalid_connection", "this address cannot be used for a connected instance")
		}
		if private {
			if !allowPrivate {
				return nil, connectionError(400, "private_address_requires_approval", "this instance uses a private address; explicitly allow this destination")
			}
			out = append(out, ip.String())
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func (s *Server) connectionTransport(target connectionTarget) (http.RoundTripper, error) {
	origin, err := normalizeConnectionURL(target.BaseURL)
	if err != nil || origin != target.BaseURL {
		return nil, connectionError(400, "invalid_connection", "invalid saved instance URL")
	}
	u, _ := url.Parse(origin)
	config := &tls.Config{MinVersion: tls.VersionTLS12}
	if s.connectionTLS != nil {
		config = s.connectionTLS.Clone()
		config.InsecureSkipVerify = false
	}
	transport := &http.Transport{
		Proxy: nil, ForceAttemptHTTP2: false, TLSClientConfig: config,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 150 * time.Second,
		MaxIdleConns: 8, IdleConnTimeout: 30 * time.Second,
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		expectedPort := u.Port()
		if expectedPort == "" {
			expectedPort = "443"
		}
		if err != nil || !strings.EqualFold(host, u.Hostname()) || port != expectedPort {
			return nil, errors.New("connection transport rejected another destination")
		}
		ips, err := resolveConnectionAddresses(ctx, host)
		if err != nil {
			return nil, err
		}
		// Refuse the entire resolution if any answer violates its saved destination policy.
		for _, ip := range ips {
			private, blocked := connectionAddressClass(ip)
			if blocked || private && (!target.AllowPrivate || !slices.Contains(target.AllowedAddresses, ip.String())) {
				return nil, errors.New("connected instance address is not approved")
			}
		}
		dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
		for _, ip := range ips {
			conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if dialErr == nil {
				return conn, nil
			}
		}
		return nil, errors.New("could not connect to the registered instance")
	}
	return transport, nil
}
