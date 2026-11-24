package loopcert

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"testing"
	"time"
)

// The certificate is a leaf for loopback and nothing more: trusting it lets
// nobody sign for another host.
func TestNew(t *testing.T) {
	now := time.Date(2030, 1, 7, 12, 0, 0, 0, time.UTC)
	cert, certPEM, err := New(now)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatal("no certificate in the PEM")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if leaf.IsCA || !leaf.BasicConstraintsValid || leaf.KeyUsage&x509.KeyUsageCertSign != 0 {
		t.Fatalf("the certificate may sign others: IsCA=%v usage=%v", leaf.IsCA, leaf.KeyUsage)
	}
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != "localhost" || len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(net.IPv4(127, 0, 0, 1)) || len(leaf.URIs)+len(leaf.EmailAddresses) != 0 {
		t.Fatalf("names %v %v", leaf.DNSNames, leaf.IPAddresses)
	}
	if !leaf.NotAfter.Equal(now.Add(Life)) || leaf.NotBefore.After(now) {
		t.Fatalf("valid %v to %v", leaf.NotBefore, leaf.NotAfter)
	}
	if _, ok := cert.PrivateKey.(*ecdsa.PrivateKey); !ok {
		t.Fatalf("key %T", cert.PrivateKey)
	}
	// A client that trusts it accepts it for loopback and for nothing else.
	pool := x509.NewCertPool()
	pool.AddCert(leaf)
	for name, want := range map[string]bool{"localhost": true, "127.0.0.1": true, "api.example.com": false, "127.0.0.2": false} {
		_, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: name, CurrentTime: now})
		if (err == nil) != want {
			t.Errorf("verified for %s: %v", name, err)
		}
	}
	// A certificate for another host signed with its key is not accepted by
	// a client that trusts it.
	key := cert.PrivateKey.(*ecdsa.PrivateKey)
	forged := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "api.example.com"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), DNSNames: []string{"api.example.com"}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, forged, leaf, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	other, _ := x509.ParseCertificate(der)
	if _, err := other.Verify(x509.VerifyOptions{Roots: pool, DNSName: "api.example.com", CurrentTime: now}); err == nil {
		t.Fatal("a certificate signed by the leaf was accepted for another host")
	}
	// Two calls give two keys.
	again, _, _ := New(now)
	if again.PrivateKey.(*ecdsa.PrivateKey).Equal(key) {
		t.Fatal("the same key twice")
	}
}
