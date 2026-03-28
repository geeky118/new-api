package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

func TestBuildCodexPoolProbeAPIErrorParsesTokenInvalidated(t *testing.T) {
	prev := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	defer func() {
		common.AutomaticDisableChannelEnabled = prev
	}()

	apiErr := buildCodexPoolProbeAPIError(http.StatusUnauthorized, []byte(`{
		"error": {
			"message": "Your authentication token has been invalidated. Please try signing in again.",
			"type": "invalid_request_error",
			"code": "token_invalidated"
		}
	}`))

	if apiErr == nil {
		t.Fatal("expected api error")
	}
	if !ShouldDisableChannel(0, apiErr) {
		t.Fatal("expected token_invalidated response to trigger automatic disable")
	}
	if got := buildCodexPoolDisableReason(apiErr); got != "status_code=401, token_invalidated" {
		t.Fatalf("unexpected disable reason: %s", got)
	}
}

func TestMatchCodexPoolTokenFilePathsUsesStableIdentity(t *testing.T) {
	oldRaw := `{"type":"codex","access_token":"at-old","refresh_token":"rt-a","account_id":"acc-a","email":"a@example.com"}`
	newRaw := `{"type":"codex","access_token":"at-new","refresh_token":"rt-a","account_id":"acc-a","email":"a@example.com"}`

	records := []codexPoolTokenFileRecord{
		{
			FilePath:   "/tmp/acc-a.json",
			Identities: buildCodexPoolStatusIdentities(oldRaw),
		},
		{
			FilePath:   "/tmp/acc-b.json",
			Identities: buildCodexPoolStatusIdentities(`{"type":"codex","access_token":"at-b","refresh_token":"rt-b","account_id":"acc-b"}`),
		},
	}

	paths := matchCodexPoolTokenFilePaths(records, newRaw)
	if len(paths) != 1 {
		t.Fatalf("expected 1 matched file path, got %d", len(paths))
	}
	if paths[0] != "/tmp/acc-a.json" {
		t.Fatalf("unexpected matched file path: %s", paths[0])
	}
}
