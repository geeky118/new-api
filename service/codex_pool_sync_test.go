package service

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestCollectCodexPoolKeysFromTokenDir(t *testing.T) {
	dir := t.TempDir()

	writeTokenFile := func(name string, key CodexOAuthKey) {
		t.Helper()
		data, err := common.Marshal(key)
		if err != nil {
			t.Fatalf("marshal token failed: %v", err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write token file failed: %v", err)
		}
	}

	writeTokenFile("acc-a-old.json", CodexOAuthKey{
		Type:         "codex",
		Email:        "a@example.com",
		AccountID:    "acc-a",
		AccessToken:  "at-old",
		RefreshToken: "rt-a",
		LastRefresh:  "2026-03-26T12:00:00+08:00",
	})
	writeTokenFile("acc-a-new.json", CodexOAuthKey{
		Type:         "codex",
		Email:        "a@example.com",
		AccountID:    "acc-a",
		AccessToken:  "at-new",
		RefreshToken: "rt-a-new",
		LastRefresh:  "2026-03-26T13:00:00+08:00",
	})
	writeTokenFile("acc-b.json", CodexOAuthKey{
		Type:         "codex",
		Email:        "b@example.com",
		AccountID:    "acc-b",
		AccessToken:  "at-b",
		RefreshToken: "rt-b",
		LastRefresh:  "2026-03-26T11:00:00+08:00",
	})

	// invalid token (missing access_token)
	writeTokenFile("invalid.json", CodexOAuthKey{
		Type:      "codex",
		Email:     "invalid@example.com",
		AccountID: "acc-invalid",
	})

	keys, stats, err := collectCodexPoolKeysFromTokenDir(dir)
	if err != nil {
		t.Fatalf("collect keys failed: %v", err)
	}

	if stats.FilesTotal != 4 {
		t.Fatalf("unexpected files total: %d", stats.FilesTotal)
	}
	if stats.FilesInvalid != 1 {
		t.Fatalf("unexpected invalid files: %d", stats.FilesInvalid)
	}
	if len(keys) != 2 {
		t.Fatalf("unexpected keys count: %d", len(keys))
	}

	var parsed []CodexOAuthKey
	for _, raw := range keys {
		var key CodexOAuthKey
		if err = common.Unmarshal([]byte(raw), &key); err != nil {
			t.Fatalf("unmarshal key failed: %v", err)
		}
		parsed = append(parsed, key)
	}

	var foundANew bool
	for _, key := range parsed {
		if key.AccountID == "acc-a" && key.AccessToken == "at-new" {
			foundANew = true
		}
	}
	if !foundANew {
		t.Fatalf("newest token for account acc-a was not selected")
	}
}

