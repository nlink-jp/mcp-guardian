package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"time"
)

// generateLoopbackCert creates an ephemeral self-signed TLS
// certificate for the local OAuth2 callback server. It is held only
// in memory — no disk persistence — and is regenerated on every
// --login invocation.
//
// SANs cover the two ways a browser might reach the listener:
//
//   - IP addresses 127.0.0.1 and ::1 (loopback IP literals)
//   - DNS name "localhost" (the more commonly accepted form in
//     provider OAuth-app redirect-URI allow-lists)
//
// Browsers will display a "not secure" warning the first time the
// user lands on https://localhost:<port>/callback because the cert
// chains to no public CA. Clicking through is expected. This is
// strictly better than the alternative for providers that reject
// http:// redirects (notably Slack) — at-rest TLS on a loopback
// listener has no realistic MITM threat, and the cert never leaves
// memory.
//
// Validity is one hour, which is comfortably longer than any
// realistic interactive --login flow. ECDSA P-256 is the key type
// (small, fast, no questions about RSA key sizes).
//
// Design rationale: docs/en/adr/0001-pre-registered-oauth-client.md
// §Decision §4.
func generateLoopbackCert() (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate serial: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "mcp-guardian local OAuth callback"},
		NotBefore:    time.Now().Add(-time.Minute), // tolerate small client-server skew
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
		DNSNames:     []string{"localhost"},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("create cert: %w", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  key,
		Leaf:        &template,
	}, nil
}
