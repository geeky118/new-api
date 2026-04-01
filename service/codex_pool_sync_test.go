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

func TestRemapCodexPoolKeyStatusByAccountID(t *testing.T) {
	oldKey, err := common.Marshal(CodexOAuthKey{
		Type:         "codex",
		Email:        "a@example.com",
		AccountID:    "acc-a",
		AccessToken:  "at-old",
		RefreshToken: "rt-old",
		LastRefresh:  "2026-03-26T12:00:00+08:00",
	})
	if err != nil {
		t.Fatalf("marshal old key failed: %v", err)
	}
	newKey, err := common.Marshal(CodexOAuthKey{
		Type:         "codex",
		Email:        "a@example.com",
		AccountID:    "acc-a",
		AccessToken:  "at-new",
		RefreshToken: "rt-new",
		LastRefresh:  "2026-03-26T13:00:00+08:00",
	})
	if err != nil {
		t.Fatalf("marshal new key failed: %v", err)
	}

	status, disabledTime, disabledReason := remapCodexPoolKeyStatus(
		[]string{string(oldKey)},
		map[int]int{0: common.ChannelStatusAutoDisabled},
		map[int]int64{0: 123456},
		map[int]string{0: "status_code=401, token_invalidated"},
		[]string{string(newKey)},
	)

	if got := status[0]; got != common.ChannelStatusAutoDisabled {
		t.Fatalf("expected remapped status %d, got %d", common.ChannelStatusAutoDisabled, got)
	}
	if got := disabledTime[0]; got != 123456 {
		t.Fatalf("expected remapped disabled time 123456, got %d", got)
	}
	if got := disabledReason[0]; got != "status_code=401, token_invalidated" {
		t.Fatalf("expected remapped disabled reason, got %q", got)
	}
}

func TestRemapCodexPoolKeyStatusByRefreshTokenFallback(t *testing.T) {
	oldKey := `{"type":"codex","access_token":"at-old","refresh_token":"rt-a"}`
	newKey := `{"type":"codex","access_token":"at-new","refresh_token":"rt-a"}`

	status, disabledTime, disabledReason := remapCodexPoolKeyStatus(
		[]string{oldKey},
		map[int]int{0: common.ChannelStatusAutoDisabled},
		map[int]int64{0: 654321},
		map[int]string{0: "manual disable"},
		[]string{newKey},
	)

	if got := status[0]; got != common.ChannelStatusAutoDisabled {
		t.Fatalf("expected remapped status %d, got %d", common.ChannelStatusAutoDisabled, got)
	}
	if got := disabledTime[0]; got != 654321 {
		t.Fatalf("expected remapped disabled time 654321, got %d", got)
	}
	if got := disabledReason[0]; got != "manual disable" {
		t.Fatalf("expected remapped disabled reason, got %q", got)
	}
}

func TestFilterCodexPoolKeysByRejectedIdentities(t *testing.T) {
	keys := []string{
		`{"type":"codex","email":"a@example.com","account_id":"acc-a","access_token":"at-a","refresh_token":"rt-a"}`,
		`{"type":"codex","email":"b@example.com","account_id":"acc-b","access_token":"at-b","refresh_token":"rt-b"}`,
	}

	filtered, skipped := filterCodexPoolKeysByRejectedIdentities(keys, map[string]struct{}{
		"account:acc-a": {},
	})

	if skipped != 1 {
		t.Fatalf("expected skipped=1, got %d", skipped)
	}
	if len(filtered) != 1 {
		t.Fatalf("expected 1 remaining key, got %d", len(filtered))
	}
	if filtered[0] != keys[1] {
		t.Fatalf("expected remaining key to be second key, got %q", filtered[0])
	}
}

func TestExtractCodexPoolRejectedIdentities(t *testing.T) {
	got := extractCodexPoolRejectedIdentities([]interface{}{"account:acc-a", "", 123, "email:a@example.com"})
	if len(got) != 2 {
		t.Fatalf("expected 2 valid identities, got %d", len(got))
	}
	if got[0] != "account:acc-a" || got[1] != "email:a@example.com" {
		t.Fatalf("unexpected identities: %#v", got)
	}
}

func TestMergeCodexPoolKeysKeepsExistingPool(t *testing.T) {
	existing := []string{
		`{"type":"codex","email":"a@example.com","account_id":"acc-a","access_token":"at-a","refresh_token":"rt-a"}`,
		`{"type":"codex","email":"b@example.com","account_id":"acc-b","access_token":"at-b","refresh_token":"rt-b"}`,
	}
	imported := []string{
		`{"type":"codex","email":"c@example.com","account_id":"acc-c","access_token":"at-c","refresh_token":"rt-c"}`,
	}

	merged, importedCount := mergeCodexPoolKeys(existing, imported, nil)
	if importedCount != 1 {
		t.Fatalf("expected importedCount=1, got %d", importedCount)
	}
	if len(merged) != 3 {
		t.Fatalf("expected merged key count 3, got %d", len(merged))
	}
	if merged[0] != existing[0] || merged[1] != existing[1] || merged[2] != imported[0] {
		t.Fatalf("unexpected merged keys: %#v", merged)
	}
}

func TestMergeCodexPoolKeysReplacesSameIdentity(t *testing.T) {
	existing := []string{
		`{"type":"codex","email":"a@example.com","account_id":"acc-a","access_token":"at-old","refresh_token":"rt-old"}`,
		`{"type":"codex","email":"b@example.com","account_id":"acc-b","access_token":"at-b","refresh_token":"rt-b"}`,
	}
	imported := []string{
		`{"type":"codex","email":"a@example.com","account_id":"acc-a","access_token":"at-new","refresh_token":"rt-new"}`,
	}

	merged, importedCount := mergeCodexPoolKeys(existing, imported, nil)
	if importedCount != 1 {
		t.Fatalf("expected importedCount=1, got %d", importedCount)
	}
	if len(merged) != 2 {
		t.Fatalf("expected merged key count 2, got %d", len(merged))
	}
	if merged[0] != imported[0] {
		t.Fatalf("expected first key to be replaced by imported key, got %q", merged[0])
	}
	if merged[1] != existing[1] {
		t.Fatalf("expected second key to stay unchanged, got %q", merged[1])
	}
}
