package webpush

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"
)

func TestServiceAddressPolicy(t *testing.T) {
	for _, raw := range []string{
		"0.0.0.0", "0.1.2.3", "10.1.2.3", "100.64.0.1", "100.127.255.255", "127.0.0.1", "169.254.169.254", "172.16.0.1", "192.168.1.2", "192.0.0.9", "192.0.2.1", "192.88.99.1", "198.18.0.1", "198.51.100.1", "203.0.113.1", "224.0.0.1", "240.0.0.1", "255.255.255.255",
		"::", "::1", "::ffff:8.8.8.8", "::ffff:127.0.0.1", "64:ff9b::7f00:1", "100::1", "2001::1", "2001:2::1", "2001:20::1", "2001:db8::1", "2002:7f00:1::", "3fff::1", "fc00::1", "fe80::1", "ff02::1", "fe80::1%eth0",
	} {
		if publicAddress(netip.MustParseAddr(raw)) {
			t.Errorf("Reserved address accepted: %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "1.1.1.1", "142.250.191.202", "2001:4860:4860::8888", "2606:4700:4700::1111"} {
		if !publicAddress(netip.MustParseAddr(raw)) {
			t.Errorf("Public address rejected: %s", raw)
		}
	}
}

func TestMixedOrInvalidDNSAnswersRejectBeforeDial(t *testing.T) {
	for _, addresses := range [][]netip.Addr{
		{}, {netip.MustParseAddr("8.8.8.8"), netip.MustParseAddr("127.0.0.1")}, {netip.MustParseAddr("::ffff:8.8.8.8")}, make([]netip.Addr, 17),
	} {
		sender := NewSender()
		sender.lookupIP = func(context.Context, string, string) ([]netip.Addr, error) { return addresses, nil }
		sender.dialContext = func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("Unsafe DNS answer was dialed")
			return nil, errors.New("unexpected dial")
		}
		_, _, err := sender.client(context.Background(), "fcm.googleapis.com")
		if !errors.Is(err, errUnsafeAddress) {
			t.Errorf("Unsafe DNS answers accepted: %v", addresses)
		}
	}
}

func serviceFixture(t *testing.T, handler http.Handler) (*Sender, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "AXV Web Push test"}, DNSNames: []string{"fcm.googleapis.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pair, err := tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: private}))
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	t.Cleanup(server.Close)
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})) {
		t.Fatal("Unable to trust fixture")
	}
	sender := NewSender()
	sender.tlsConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	var lookups, dials atomic.Int32
	sender.lookupIP = func(_ context.Context, network, host string) ([]netip.Addr, error) {
		lookups.Add(1)
		if host != "fcm.googleapis.com" || network != "ip" {
			t.Errorf("Unexpected DNS target: %s %s", network, host)
		}
		return []netip.Addr{netip.MustParseAddr("8.8.8.8")}, nil
	}
	dialer := &net.Dialer{}
	sender.dialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		if address != "8.8.8.8:443" {
			t.Errorf("Connection was not pinned to the checked address: %s", address)
		}
		return dialer.DialContext(ctx, network, server.Listener.Addr().String())
	}
	return sender, &lookups, &dials
}

func receiveFixtureNotification(t *testing.T, r *http.Request) bool {
	t.Helper()
	// The sender closes each connection. Consume the bounded encrypted record
	// before replying so TLS closure cannot race an unfinished request write.
	size, err := io.Copy(io.Discard, io.LimitReader(r.Body, 4097))
	if err != nil || size != 4096 {
		t.Errorf("Fixture did not receive a complete Web Push record: size=%d, err=%v", size, err)
		return false
	}
	return true
}

func TestPinnedTLSServiceAndNoRedirectOrProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("ALL_PROXY", "http://127.0.0.1:1")
	subscription, _ := testSubscription(t)
	private, public, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	sender, lookups, dials := serviceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !receiveFixtureNotification(t, r) {
			return
		}
		if r.TLS.ServerName != "fcm.googleapis.com" || r.Host != "fcm.googleapis.com" {
			t.Error("TLS hostname/Host did not retain the approved service identity")
		}
		w.Header().Set("Location", "https://127.0.0.1/private")
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	if outcome := sender.Send(context.Background(), subscription, private, public, []byte(`{"type":"test"}`), time.Minute, "owner@example.test"); outcome.Status != Rejected {
		t.Fatalf("Redirect was not rejected: %v", outcome)
	}
	if lookups.Load() != 1 || dials.Load() != 1 {
		t.Fatalf("Unexpected request after redirect: %d lookups, %d dials", lookups.Load(), dials.Load())
	}
}

func TestUntrustedCertificateIsRejected(t *testing.T) {
	subscription, _ := testSubscription(t)
	private, public, err := GenerateVAPIDKeys()
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	sender, _, dials := serviceFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusCreated)
	}))
	sender.tlsConfig.RootCAs = x509.NewCertPool()
	if outcome := sender.Send(context.Background(), subscription, private, public, []byte("test"), time.Minute, "owner@example.test"); outcome.Status != Uncertain {
		t.Fatal(outcome)
	}
	if requests.Load() != 0 || dials.Load() != 1 {
		t.Fatal("Untrusted TLS request was sent")
	}
}
