package sessionpipe

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParsePoolStatus(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name      string
		raw       string
		want      PoolSummary
		wantReset time.Time
		wantErr   bool
	}{
		{
			name: "off",
			raw:  `{"on":false,"listening":false,"port":0,"in_flight":0,"accounts":[]}`,
			want: PoolSummary{Health: PoolOff, On: false},
		},
		{
			name: "pool on daemon not listening",
			raw:  `{"on":true,"listening":false,"port":47812,"in_flight":0,"accounts":[{"id":"acctaaaa","label":"fictional-a","email":"a@example.com","uuid":"11111111-1111-4111-8111-111111111111","enabled":true,"status":"ready","current":true}]}`,
			want: PoolSummary{Health: PoolDown, On: true, Listening: false, Port: 47812, TotalAccounts: 1, EnabledAccounts: 1, ReadyAccounts: 1, HasCurrent: true},
		},
		{
			name: "ready with one limited and one disabled account",
			raw:  `{"on":true,"listening":true,"port":47812,"in_flight":2,"accounts":[{"label":"fictional-a","enabled":true,"status":"limited","until":"2026-11-25T13:00:00Z"},{"label":"fictional-b","email":"b@example.com","enabled":true,"status":"ready","current":true},{"label":"fictional-c","enabled":false,"status":"ready"}]}`,
			want: PoolSummary{
				Health: PoolReady, On: true, Listening: true, Port: 47812, InFlight: 2,
				TotalAccounts: 3, EnabledAccounts: 2, ReadyAccounts: 1, LimitedAccounts: 1, DisabledAccounts: 1,
				HasCurrent: true,
			},
			wantReset: now.Add(time.Hour),
		},
		{
			name: "all enabled accounts exhausted picks earliest valid reset",
			raw:  `{"on":true,"listening":true,"port":47812,"accounts":[{"enabled":true,"status":"limited","until":"2026-11-25T15:00:00Z","windows":[{"id":"5h","used_percent":100,"resets_at":"2026-11-25T12:20:00Z"}]},{"enabled":true,"status":"limited","until":"2026-11-25T14:00:00Z"}]}`,
			want: PoolSummary{
				Health: PoolExhausted, On: true, Listening: true, Port: 47812,
				TotalAccounts: 2, EnabledAccounts: 2, LimitedAccounts: 2,
			},
			wantReset: now.Add(20 * time.Minute),
		},
		{
			name: "all enabled accounts in error secrets unreadable",
			raw:  `{"on":true,"listening":true,"port":47812,"accounts":[{"enabled":true,"status":"error"},{"enabled":true,"status":"mystery"}]}`,
			want: PoolSummary{
				Health: PoolDegraded, On: true, Listening: true, Port: 47812,
				TotalAccounts: 2, EnabledAccounts: 2, ErrorAccounts: 2,
			},
		},
		{
			name: "listening with no enabled accounts",
			raw:  `{"on":true,"listening":true,"port":47812,"accounts":[{"enabled":false,"status":"ready"},{"enabled":true,"status":"disabled"}]}`,
			want: PoolSummary{
				Health: PoolEmpty, On: true, Listening: true, Port: 47812,
				TotalAccounts: 2, DisabledAccounts: 2,
			},
		},
		{
			name: "clamps out of range port negative in_flight and absurd clock jump reset",
			raw:  `{"on":true,"listening":true,"port":99999,"in_flight":-4,"accounts":[{"enabled":true,"status":"limited","until":"2026-11-25T11:00:00Z","windows":[{"resets_at":"2035-01-01T00:00:00Z"}]}]}`,
			want: PoolSummary{
				Health: PoolExhausted, On: true, Listening: true, Port: 0, InFlight: 0,
				TotalAccounts: 1, EnabledAccounts: 1, LimitedAccounts: 1,
			},
		},
		{name: "empty", raw: "", wantErr: true},
		{name: "missing on field", raw: `{"listening":true}`, wantErr: true},
		{name: "corrupt json", raw: `{"on":true`, wantErr: true},
		{name: "trailing json", raw: `{"on":false} {}`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParsePoolStatus([]byte(tc.raw), now)
			if tc.wantErr {
				if !errors.Is(err, ErrPoolStatus) {
					t.Fatalf("got (%+v, %v), want ErrPoolStatus", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.wantReset.IsZero() {
				if got.EarliestReset == nil || !got.EarliestReset.Equal(tc.wantReset) {
					t.Fatalf("EarliestReset = %v, want %v", got.EarliestReset, tc.wantReset)
				}
				got.EarliestReset = nil
			} else if got.EarliestReset != nil {
				t.Fatalf("EarliestReset = %v, want nil", got.EarliestReset)
			}
			if got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
			// Redaction check: serialized summary never retains emails, labels or UUIDs.
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			for _, secret := range []string{"example.com", "fictional-", "11111111", "acctaaaa"} {
				if strings.Contains(string(b), secret) {
					t.Fatalf("summary leaked %q: %s", secret, b)
				}
			}
		})
	}
}

func TestParsePoolStatusRejectsOversizedPayload(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	big := `{"on":false,"pad":"` + strings.Repeat("a", MaxPoolStatusBytes) + `"}`
	if _, err := ParsePoolStatus([]byte(big), now); !errors.Is(err, ErrPoolStatus) {
		t.Fatalf("got %v, want ErrPoolStatus", err)
	}
}

func TestSettingsUsesPoolPort(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		port    int
		want    bool
		wantErr bool
	}{
		{
			name: "127.0.0.1 matching port",
			raw:  `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:47812","ANTHROPIC_AUTH_TOKEN":"fictional-bearer"}}`,
			port: 47812,
			want: true,
		},
		{
			name: "localhost https matching port",
			raw:  `{"env":{"ANTHROPIC_BASE_URL":"https://LOCALHOST:47812/"}}`,
			port: 47812,
			want: true,
		},
		{
			name: "ipv6 loopback matching port",
			raw:  `{"env":{"ANTHROPIC_BASE_URL":"http://[::1]:47812"}}`,
			port: 47812,
			want: true,
		},
		{
			name: "different port",
			raw:  `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:51725"}}`,
			port: 47812,
			want: false,
		},
		{
			name: "remote host rejected",
			raw:  `{"env":{"ANTHROPIC_BASE_URL":"https://api.example.com:47812"}}`,
			port: 47812,
			want: false,
		},
		{
			name: "userinfo in url rejected",
			raw:  `{"env":{"ANTHROPIC_BASE_URL":"http://user:pass@127.0.0.1:47812"}}`,
			port: 47812,
			want: false,
		},
		{
			name: "no env block",
			raw:  `{"theme":"dark"}`,
			port: 47812,
			want: false,
		},
		{
			name: "null env block",
			raw:  `{"env":null}`,
			port: 47812,
			want: false,
		},
		{
			name:    "non object settings",
			raw:     `[]`,
			port:    47812,
			wantErr: true,
		},
		{
			name:    "env not object",
			raw:     `{"env":"bad"}`,
			port:    47812,
			wantErr: true,
		},
		{
			name:    "trailing json",
			raw:     `{} {}`,
			port:    47812,
			wantErr: true,
		},
		{
			name: "invalid port argument",
			raw:  `{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:47812"}}`,
			port: 0,
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SettingsUsesPoolPort([]byte(tc.raw), tc.port)
			if tc.wantErr {
				if !errors.Is(err, ErrSettingsJSON) {
					t.Fatalf("got (%v, %v), want ErrSettingsJSON", got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSettingsFileUsesPoolPort(t *testing.T) {
	dir := t.TempDir()

	// Missing file is not an error and not pointed.
	got, err := SettingsFileUsesPoolPort(filepath.Join(dir, "missing.json"), 47812)
	if err != nil || got {
		t.Fatalf("missing file: (%v, %v)", got, err)
	}

	// Regular settings file matching port.
	p := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(p, []byte(`{"env":{"ANTHROPIC_BASE_URL":"http://127.0.0.1:47812"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = SettingsFileUsesPoolPort(p, 47812)
	if err != nil || !got {
		t.Fatalf("regular file: (%v, %v)", got, err)
	}

	// Non-regular target (directory) is refused without reading.
	sub := filepath.Join(dir, "subdir")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := SettingsFileUsesPoolPort(sub, 47812); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("directory: %v, want ErrNotRegular", err)
	}
}
