// Package loopcert makes the certificate the account router's front serves
// on loopback (docs/design/router.md, "The loopback certificate"): a
// self-signed leaf that cannot sign others and is valid only for loopback
// names. It is the one thing a pointed client is told to trust, so a
// program without its key cannot stand in for the front.
package loopcert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"time"
)

// Life is how long a certificate is valid. The front makes a new one before
// then.
const Life = 397 * 24 * time.Hour

// New makes a certificate at now. certPEM is what a client is given to
// trust; the key stays in the returned tls.Certificate and is the caller's
// to keep private.
func New(now time.Time) (cert tls.Certificate, certPEM []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	tpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "looptender router front (loopback)"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(Life),
		// A leaf: it may identify a server and nothing else. It is not a
		// certificate authority and cannot sign another certificate.
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}
