package service

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
)

func TestShouldDisableChannelWithTokenInvalidated(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	defer func() {
		common.AutomaticDisableChannelEnabled = previous
	}()

	err := types.WithOpenAIError(types.OpenAIError{
		Message: "Your authentication token has been invalidated. Please try signing in again.",
		Type:    "invalid_request_error",
		Code:    "token_invalidated",
	}, http.StatusUnauthorized)

	if !ShouldDisableChannel(0, err) {
		t.Fatal("expected token_invalidated to trigger automatic disable")
	}
}

func TestShouldDisableChannelWithUsageLimitReached(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	defer func() {
		common.AutomaticDisableChannelEnabled = previous
	}()

	err := types.WithOpenAIError(types.OpenAIError{
		Message: "You've hit your usage limit. Try again later.",
		Type:    "usage_limit_reached",
		Code:    "usage_limit_reached",
	}, http.StatusTooManyRequests)

	if !ShouldDisableChannel(0, err) {
		t.Fatal("expected usage_limit_reached to trigger automatic disable")
	}
}

func TestShouldRemoveChannelKeyFromPoolForCodexMultiKey(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	defer func() {
		common.AutomaticDisableChannelEnabled = previous
	}()

	channelErr := *types.NewChannelError(1, constant.ChannelTypeCodex, "codex-pool", true, `{"account_id":"acc-1","access_token":"at-1"}`, true)
	apiErr := types.WithOpenAIError(types.OpenAIError{
		Message: "Your authentication token has been invalidated.",
		Type:    "invalid_request_error",
		Code:    "token_invalidated",
	}, http.StatusUnauthorized)

	if !ShouldRemoveChannelKeyFromPool(channelErr, apiErr) {
		t.Fatal("expected codex multi-key auto-ban error to trigger permanent removal")
	}
}

func TestShouldNotRemoveChannelKeyFromPoolForNonCodex(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	defer func() {
		common.AutomaticDisableChannelEnabled = previous
	}()

	channelErr := *types.NewChannelError(1, 1, "openai-pool", true, "sk-xxx", true)
	apiErr := types.WithOpenAIError(types.OpenAIError{
		Message: "You've hit your usage limit. Try again later.",
		Type:    "usage_limit_reached",
		Code:    "usage_limit_reached",
	}, http.StatusTooManyRequests)

	if ShouldRemoveChannelKeyFromPool(channelErr, apiErr) {
		t.Fatal("did not expect non-codex channel to trigger permanent pool removal")
	}
}
