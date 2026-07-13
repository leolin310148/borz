package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/leolin310148/borz/internal/config"
)

func setHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.HomeEnv, home)
	return home
}

func writeProfiles(t *testing.T, home, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "profiles.json"), []byte(content), 0o600); err != nil {
		t.Fatalf("write profiles.json: %v", err)
	}
}

func TestResolveTargetMissingFileIsManaged(t *testing.T) {
	setHome(t)
	for _, name := range []string{"", "default", "unknown"} {
		target, err := ResolveTarget(name)
		if err != nil {
			t.Fatalf("ResolveTarget(%q): %v", name, err)
		}
		if target.Kind != TransportManaged {
			t.Fatalf("ResolveTarget(%q).Kind = %q, want managed", name, target.Kind)
		}
	}
}

func TestResolveTargetMissingEntryIsManaged(t *testing.T) {
	home := setHome(t)
	writeProfiles(t, home, `{"version":1,"profiles":{"mini":{"transport":"remote","url":"http://10.0.0.1:13333"}}}`)
	target, err := ResolveTarget("other")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.Kind != TransportManaged {
		t.Fatalf("missing entry Kind = %q, want managed", target.Kind)
	}
}

func TestResolveTargetManaged(t *testing.T) {
	home := setHome(t)
	writeProfiles(t, home, `{"version":1,"profiles":{"clean":{"transport":"managed"}}}`)
	target, err := ResolveTarget("clean")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.Kind != TransportManaged {
		t.Fatalf("Kind = %q, want managed", target.Kind)
	}
}

func TestResolveTargetRemote(t *testing.T) {
	home := setHome(t)
	writeProfiles(t, home, `{"version":1,"profiles":{"mini":{"transport":"remote","url":"100.116.143.73:13333/","token":" secret "}}}`)
	target, err := ResolveTarget("mini")
	if err != nil {
		t.Fatalf("ResolveTarget: %v", err)
	}
	if target.Kind != TransportRemote || target.Remote.URL != "http://100.116.143.73:13333" || target.Remote.Token != "secret" {
		t.Fatalf("remote target = %+v", target)
	}
}

func TestResolveTargetRemoteMissingURL(t *testing.T) {
	home := setHome(t)
	writeProfiles(t, home, `{"version":1,"profiles":{"mini":{"transport":"remote"}}}`)
	if _, err := ResolveTarget("mini"); err == nil || !strings.Contains(err.Error(), `profile "mini"`) {
		t.Fatalf("ResolveTarget error = %v, want profile-scoped URL error", err)
	}
}

func TestResolveTargetCDPSpellings(t *testing.T) {
	home := setHome(t)
	tests := []struct {
		name  string
		entry string
		want  CDPTarget
	}{
		{"url", `{"transport":"cdp","cdpUrl":"http://127.0.0.1:19845"}`, CDPTarget{Host: "127.0.0.1", Port: 19845}},
		{"bare url", `{"transport":"cdp","cdpUrl":"tunnel.host:9222"}`, CDPTarget{Host: "tunnel.host", Port: 9222}},
		{"host port", `{"transport":"cdp","cdpHost":"10.1.2.3","cdpPort":9222}`, CDPTarget{Host: "10.1.2.3", Port: 9222}},
		{"port only defaults host", `{"transport":"cdp","cdpPort":9222}`, CDPTarget{Host: "127.0.0.1", Port: 9222}},
		{"both consistent", `{"transport":"cdp","cdpUrl":"http://10.1.2.3:9222","cdpHost":"10.1.2.3","cdpPort":9222}`, CDPTarget{Host: "10.1.2.3", Port: 9222}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writeProfiles(t, home, `{"version":1,"profiles":{"mdt":`+tc.entry+`}}`)
			target, err := ResolveTarget("mdt")
			if err != nil {
				t.Fatalf("ResolveTarget: %v", err)
			}
			if target.Kind != TransportCDP || target.CDP != tc.want {
				t.Fatalf("cdp target = %+v, want %+v", target, tc.want)
			}
		})
	}
}

func TestResolveTargetCDPErrors(t *testing.T) {
	home := setHome(t)
	tests := []struct {
		name    string
		entry   string
		wantErr string
	}{
		{"no endpoint", `{"transport":"cdp"}`, "requires cdpUrl or cdpHost/cdpPort"},
		{"inconsistent spellings", `{"transport":"cdp","cdpUrl":"http://127.0.0.1:9222","cdpHost":"127.0.0.1","cdpPort":9333}`, "disagree"},
		{"bad url", `{"transport":"cdp","cdpUrl":"ftp://x:1"}`, "invalid cdpUrl"},
		{"host without port", `{"transport":"cdp","cdpHost":"10.0.0.1"}`, "cdpPort must be"},
		{"port out of range", `{"transport":"cdp","cdpHost":"10.0.0.1","cdpPort":70000}`, "cdpPort must be"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			writeProfiles(t, home, `{"version":1,"profiles":{"mdt":`+tc.entry+`}}`)
			_, err := ResolveTarget("mdt")
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("ResolveTarget error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestResolveTargetTransportErrors(t *testing.T) {
	home := setHome(t)
	writeProfiles(t, home, `{"version":1,"profiles":{"a":{"transport":"warp"},"b":{}}}`)
	if _, err := ResolveTarget("a"); err == nil || !strings.Contains(err.Error(), "unknown transport") {
		t.Fatalf("unknown transport error = %v", err)
	}
	if _, err := ResolveTarget("b"); err == nil || !strings.Contains(err.Error(), "missing transport") {
		t.Fatalf("missing transport error = %v", err)
	}
}

func TestLoadMalformedAndFutureVersion(t *testing.T) {
	home := setHome(t)
	writeProfiles(t, home, `{not json`)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("malformed load error = %v", err)
	}
	writeProfiles(t, home, `{"version":99,"profiles":{}}`)
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "version 99") {
		t.Fatalf("future version error = %v", err)
	}
}

func TestSaveRoundTripAndPermissions(t *testing.T) {
	home := setHome(t)
	f := &File{Profiles: map[string]Entry{
		"mini": {Transport: "remote", URL: "http://10.0.0.1:13333", Token: "tok"},
	}}
	if err := Save(f); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if f.Version != CurrentVersion {
		t.Fatalf("Save did not stamp version, got %d", f.Version)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Version != CurrentVersion || loaded.Profiles["mini"].URL != "http://10.0.0.1:13333" {
		t.Fatalf("round trip = %+v", loaded)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(home, "profiles.json"))
		if err != nil {
			t.Fatalf("stat profiles.json: %v", err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("profiles.json mode = %v, want 0600", st.Mode().Perm())
		}
	}
	if entries, err := filepath.Glob(filepath.Join(home, ".profiles-*.json")); err != nil || len(entries) != 0 {
		t.Fatalf("temp files left behind: %v (err %v)", entries, err)
	}
}

func TestSaveNilFile(t *testing.T) {
	setHome(t)
	if err := Save(nil); err == nil {
		t.Fatal("Save(nil) should error")
	}
}

func TestMigrateClientJSON(t *testing.T) {
	home := setHome(t)
	clientJSON := `{"url":"http://100.116.143.73:13333/","token":"tok","enabled":true}`
	if err := os.WriteFile(filepath.Join(home, "client.json"), []byte(clientJSON), 0o600); err != nil {
		t.Fatalf("write client.json: %v", err)
	}

	target, err := ResolveTarget(LegacyRemoteName)
	if err != nil {
		t.Fatalf("ResolveTarget after migration: %v", err)
	}
	if target.Kind != TransportRemote || target.Remote.URL != "http://100.116.143.73:13333" || target.Remote.Token != "tok" {
		t.Fatalf("migrated target = %+v", target)
	}

	// client.json stays on disk for rollback.
	if _, err := os.Stat(filepath.Join(home, "client.json")); err != nil {
		t.Fatalf("client.json should survive migration: %v", err)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(filepath.Join(home, "profiles.json"))
		if err != nil {
			t.Fatalf("stat migrated profiles.json: %v", err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("migrated profiles.json mode = %v, want 0600", st.Mode().Perm())
		}
	}

	// The 'enabled' field is dead and must not be carried over.
	raw, err := os.ReadFile(filepath.Join(home, "profiles.json"))
	if err != nil {
		t.Fatalf("read migrated profiles.json: %v", err)
	}
	if strings.Contains(string(raw), "enabled") {
		t.Fatalf("migration carried the dead enabled field:\n%s", raw)
	}

	// Idempotent: a second run keeps the file byte-identical.
	MigrateClientJSON()
	again, err := os.ReadFile(filepath.Join(home, "profiles.json"))
	if err != nil {
		t.Fatalf("re-read profiles.json: %v", err)
	}
	if string(raw) != string(again) {
		t.Fatalf("migration is not idempotent:\nfirst:\n%s\nsecond:\n%s", raw, again)
	}
}

func TestMigrateClientJSONNeverClobbersExistingRegistry(t *testing.T) {
	home := setHome(t)
	existing := `{"version":1,"profiles":{"remote":{"transport":"remote","url":"http://keep.me:1111"}}}`
	writeProfiles(t, home, existing)
	if err := os.WriteFile(filepath.Join(home, "client.json"), []byte(`{"url":"http://clobber.me:2222"}`), 0o600); err != nil {
		t.Fatalf("write client.json: %v", err)
	}
	MigrateClientJSON()
	raw, err := os.ReadFile(filepath.Join(home, "profiles.json"))
	if err != nil {
		t.Fatalf("read profiles.json: %v", err)
	}
	if string(raw) != existing {
		t.Fatalf("existing profiles.json was clobbered:\n%s", raw)
	}
}

func TestMigrateClientJSONSkipsMissingOrInvalidClientJSON(t *testing.T) {
	home := setHome(t)
	MigrateClientJSON() // no client.json at all
	if _, err := os.Stat(filepath.Join(home, "profiles.json")); !os.IsNotExist(err) {
		t.Fatalf("migration without client.json should not create profiles.json: %v", err)
	}

	for _, content := range []string{`{broken`, `{"url":""}`, `{"url":"ftp://bad:1"}`} {
		if err := os.WriteFile(filepath.Join(home, "client.json"), []byte(content), 0o600); err != nil {
			t.Fatalf("write client.json: %v", err)
		}
		MigrateClientJSON()
		if _, err := os.Stat(filepath.Join(home, "profiles.json")); !os.IsNotExist(err) {
			t.Fatalf("migration from %q should not create profiles.json: %v", content, err)
		}
	}
}

func TestNormalize(t *testing.T) {
	if Normalize("") != DefaultName || Normalize("  ") != DefaultName || Normalize("mdt") != "mdt" {
		t.Fatal("Normalize mapping is wrong")
	}
}

func TestNormalizeServerURL(t *testing.T) {
	tests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"http://x.test:1234/", "http://x.test:1234", false},
		{"x.test:1234", "http://x.test:1234", false},
		{"https://x.test/path/?q=1#f", "https://x.test/path", false},
		{"", "", true},
		{"ftp://x.test", "", true},
		{"http://", "", true},
		{"http://user:pw@x.test", "", true},
		{"http://x.test:99999", "", true},
		{"x.test:", "", true},
	}
	for _, tc := range tests {
		got, err := NormalizeServerURL(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeServerURL(%q) should error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("NormalizeServerURL(%q) = %q, %v; want %q", tc.in, got, err, tc.want)
		}
	}
}

func TestParseCDPEndpoint(t *testing.T) {
	tests := []struct {
		in       string
		wantHost string
		wantPort int
		wantOK   bool
	}{
		{"http://127.0.0.1:9222", "127.0.0.1", 9222, true},
		{"ws://h.test:9222", "h.test", 9222, true},
		{"h.test:9222", "h.test", 9222, true},
		{"", "", 0, false},
		{"ftp://h.test:9222", "", 0, false},
		{"h.test", "", 0, false},
		{"h.test:0", "", 0, false},
		{"h.test:70000", "", 0, false},
		{"http://:9222", "", 0, false},
	}
	for _, tc := range tests {
		host, port, ok := ParseCDPEndpoint(tc.in)
		if ok != tc.wantOK || host != tc.wantHost || port != tc.wantPort {
			t.Errorf("ParseCDPEndpoint(%q) = %q, %d, %v; want %q, %d, %v", tc.in, host, port, ok, tc.wantHost, tc.wantPort, tc.wantOK)
		}
	}
}

func TestFileJSONShapeMatchesDesign(t *testing.T) {
	f := &File{Version: 1, Profiles: map[string]Entry{
		"mdt": {Transport: "cdp", CDPURL: "http://127.0.0.1:19845"},
	}}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"version":1`, `"transport":"cdp"`, `"cdpUrl":"http://127.0.0.1:19845"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("serialized file missing %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "cdpHost") || strings.Contains(string(data), "token") {
		t.Fatalf("zero fields should be omitted:\n%s", data)
	}
}
