package web

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
)

func TestConnectionTransportOriginAndAddresses(t *testing.T) {
	for _, raw := range []string{"http://uam.example", "https://user:secret@uam.example", "https://uam.example/api", "https://uam.example?token=x", "https://uam.example#x", "https://uam.example:99999", "https://[fe80::1%25eth0]"} {
		if _, err := normalizeConnectionURL(raw); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if got, err := normalizeConnectionURL(" https://UAM.example:443/ "); err != nil || got != "https://uam.example" {
		t.Fatalf("normalize=%q %v", got, err)
	}
	for _, raw := range []string{"0.0.0.0", "::", "169.254.169.254", "fe80::1", "224.0.0.1", "100.100.100.200", "fd00:ec2::254"} {
		if _, blocked := connectionAddressClass(netip.MustParseAddr(raw)); !blocked {
			t.Errorf("allowed forbidden address %s", raw)
		}
	}
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "192.168.1.2", "::1", "100.64.0.1"} {
		private, blocked := connectionAddressClass(netip.MustParseAddr(raw))
		if !private || blocked {
			t.Errorf("private address classification %s", raw)
		}
	}
}

func TestConnectionTransportTLSAndPinnedPrivateDestination(t *testing.T) {
	var hits atomic.Int32
	host := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits.Add(1); w.WriteHeader(204) }))
	defer host.Close()
	target := targetFixture()
	target.BaseURL = host.URL
	target.AllowPrivate = true
	allowed, err := connectionAddresses(context.Background(), target.BaseURL, true)
	if err != nil {
		t.Fatal(err)
	}
	target.AllowedAddresses = allowed
	req, _ := http.NewRequest("GET", host.URL, nil)
	s := &Server{}
	transport, _ := s.connectionTransport(target)
	if response, err := transport.RoundTrip(req); err == nil {
		_ = response.Body.Close()
		t.Fatal("untrusted target TLS accepted")
	}
	roots := x509.NewCertPool()
	roots.AddCert(host.Certificate())
	s.connectionTLS = &tls.Config{RootCAs: roots}
	target.AllowedAddresses = nil
	transport, _ = s.connectionTransport(target)
	if response, err := transport.RoundTrip(req); err == nil {
		_ = response.Body.Close()
		t.Fatal("unapproved private address dialed")
	}
	if hits.Load() != 0 {
		t.Fatal("rejected destination received a request")
	}
	target.AllowedAddresses = allowed
	transport, _ = s.connectionTransport(target)
	defer transport.(*http.Transport).CloseIdleConnections()
	response, err := transport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 204 || hits.Load() != 1 {
		t.Fatal("approved verified target not reached")
	}
	other, _ := http.NewRequest("GET", strings.Replace(host.URL, "127.0.0.1", "localhost", 1), nil)
	if response, err := transport.RoundTrip(other); err == nil {
		_ = response.Body.Close()
		t.Fatal("transport dialed unregistered origin")
	}
}

func TestConnectionPairDoesNotFollowRedirect(t *testing.T) {
	var redirected atomic.Int32
	destination := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1); w.WriteHeader(500) }))
	defer destination.Close()
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	home := newTestServer(t, ServerConfig{})
	roots := x509.NewCertPool()
	roots.AddCert(origin.Certificate())
	home.srv.connectionTLS = &tls.Config{RootCAs: roots}
	target := targetFixture()
	target.BaseURL = origin.URL
	target.AllowPrivate = true
	target.InstanceID = ""
	if _, _, err := home.srv.pairConnection(context.Background(), target, testToken); err == nil {
		t.Fatal("redirect pairing succeeded")
	}
	if redirected.Load() != 0 {
		t.Fatal("pairing forwarded master token to redirect")
	}
}
