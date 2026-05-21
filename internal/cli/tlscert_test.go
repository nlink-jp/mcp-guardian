package cli

import (
	"crypto/x509"
	"net"
	"testing"
	"time"
)

// TestGenerateLoopbackCert verifies that the cert produced is
// suitable for a local OAuth callback listener — it must cover both
// the loopback IP literals and the DNS name "localhost" so providers
// that accept either form will pass redirect_uri validation.
func TestGenerateLoopbackCert(t *testing.T) {
	cert, err := generateLoopbackCert()
	if err != nil {
		t.Fatalf("generateLoopbackCert: %v", err)
	}

	if len(cert.Certificate) != 1 {
		t.Fatalf("cert chain length=%d, want 1 (self-signed leaf only)", len(cert.Certificate))
	}

	parsed, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}

	// IP SANs cover 127.0.0.1 and ::1
	wantIPs := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	for _, want := range wantIPs {
		found := false
		for _, got := range parsed.IPAddresses {
			if got.Equal(want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("IP SAN missing %s; got %v", want, parsed.IPAddresses)
		}
	}

	// DNS SAN covers "localhost"
	wantDNS := "localhost"
	found := false
	for _, got := range parsed.DNSNames {
		if got == wantDNS {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("DNS SAN missing %q; got %v", wantDNS, parsed.DNSNames)
	}

	// Validity window covers "now" — start is slightly in the past
	// (skew tolerance) and end is well into the future.
	now := time.Now()
	if !now.After(parsed.NotBefore) {
		t.Errorf("NotBefore=%v is not before now=%v", parsed.NotBefore, now)
	}
	if !now.Before(parsed.NotAfter) {
		t.Errorf("NotAfter=%v is not after now=%v", parsed.NotAfter, now)
	}

	// ExtKeyUsage must include server auth — otherwise Go's TLS
	// stack will refuse to use this cert for a server.
	hasServerAuth := false
	for _, eku := range parsed.ExtKeyUsage {
		if eku == x509.ExtKeyUsageServerAuth {
			hasServerAuth = true
			break
		}
	}
	if !hasServerAuth {
		t.Errorf("cert missing ExtKeyUsageServerAuth; got %v", parsed.ExtKeyUsage)
	}
}
