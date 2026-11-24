package pointer

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"sync"
)

// Clients knows each client's secret by its digest only. A client is one
// settings file a pointer was written to. The secret itself exists in the
// pointer and, for a moment, in the value Issue returns; it is not a
// provider's credential and cannot become one: it only tells the front
// which client is asking.
type Clients struct {
	mu sync.Mutex
	// Digests is client id to the digest of its secret.
	Digests map[string]string `json:"digests"`
}

func newSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func secretDigest(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// Issue gives client a new secret and forgets any it had: the old one stops
// working at once.
func (c *Clients) Issue(client string) (string, error) {
	secret, err := newSecret()
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.Digests == nil {
		c.Digests = map[string]string{}
	}
	c.Digests[client] = secretDigest(secret)
	return secret, nil
}

// Rotate makes client's secret worthless. The new one is told to nobody:
// it exists so that the old one, which a session pointed back at the
// provider may still send, is dead. A later Point issues a fresh one.
func (c *Clients) Rotate(client string) error {
	_, err := c.Issue(client)
	return err
}

// Lookup returns the client a secret was issued to. It is what the front
// asks for every request.
func (c *Clients) Lookup(secret string) (client string, ok bool) {
	if secret == "" {
		return "", false
	}
	want := secretDigest(secret)
	c.mu.Lock()
	defer c.mu.Unlock()
	for id, have := range c.Digests {
		if subtle.ConstantTimeCompare([]byte(have), []byte(want)) == 1 {
			client, ok = id, true
		}
	}
	return client, ok
}
