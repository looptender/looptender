package login

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"github.com/looptender/looptender/internal/router/core"
)

const (
	codexAuthFile   = "auth.json"
	codexConfigFile = "config.toml"
)

// codexAuthPayload decodes only the access token and account id; refresh tokens are ignored.
type codexAuthPayload struct {
	Tokens *struct {
		AccessToken string `json:"access_token"`
		AccountID   string `json:"account_id"`
	} `json:"tokens"`
}

type codexJWTClaims struct {
	Exp        int64 `json:"exp"`
	OpenAIAuth *struct {
		ChatGPTAccountID string `json:"chatgpt_account_id"`
	} `json:"https://api.openai.com/auth"`
}

func (r *Reader) readCodex(sub Subscription) (core.Login, error) {
	if strings.TrimSpace(sub.Dir) == "" {
		return core.Login{}, &Error{Subscription: sub.ID, Provider: sub.Provider, Kind: ErrInvalid, Detail: "missing directory"}
	}
	if store, err := codexCredentialStore(sub.Dir, r.ownUID()); err != nil {
		return core.Login{}, wrapStoreError(sub, err)
	} else if store != "" && store != "file" {
		return core.Login{}, &Error{Subscription: sub.ID, Provider: sub.Provider, Kind: ErrUnsupportedStore, Detail: "keyring store is not supported"}
	}

	raw, err := readPrivateFile(filepath.Join(sub.Dir, codexAuthFile), r.ownUID())
	if err != nil {
		return core.Login{}, wrapStoreError(sub, err)
	}

	var doc codexAuthPayload
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Tokens == nil {
		return core.Login{}, &Error{Subscription: sub.ID, Provider: sub.Provider, Kind: ErrInvalid, Detail: "malformed auth.json"}
	}
	tok := strings.TrimSpace(doc.Tokens.AccessToken)
	if tok == "" {
		return core.Login{}, &Error{Subscription: sub.ID, Provider: sub.Provider, Kind: ErrInvalid, Detail: "missing access token"}
	}
	claims, err := parseJWTClaims(tok)
	if err != nil || claims.Exp <= 0 {
		return core.Login{}, &Error{Subscription: sub.ID, Provider: sub.Provider, Kind: ErrInvalid, Detail: "missing or invalid token expiry"}
	}

	account := strings.TrimSpace(doc.Tokens.AccountID)
	if account == "" && claims.OpenAIAuth != nil {
		account = strings.TrimSpace(claims.OpenAIAuth.ChatGPTAccountID)
	}
	if account == "" {
		account = sub.Account
	}
	return core.Login{
		Authorization: "Bearer " + tok,
		Account:       account,
		Expires:       time.Unix(claims.Exp, 0),
	}, nil
}

func parseJWTClaims(token string) (codexJWTClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		return codexJWTClaims{}, ErrInvalid
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return codexJWTClaims{}, ErrInvalid
	}
	var claims codexJWTClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return codexJWTClaims{}, ErrInvalid
	}
	return claims, nil
}

// codexCredentialStore reads cli_auth_credentials_store from config.toml if present.
func codexCredentialStore(dir string, ownUID int) (string, error) {
	b, err := readRegularFile(filepath.Join(dir, codexConfigFile), ownUID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if idx := strings.IndexByte(line, '#'); idx >= 0 {
			line = line[:idx]
		}
		line = strings.TrimSpace(line)
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) != "cli_auth_credentials_store" {
			continue
		}
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		return v, nil
	}
	return "", nil
}
