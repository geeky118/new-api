package service

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
)

type codexPoolHealthCheckConfig struct {
	Enabled            bool
	ChannelID          int
	TokenDir           string
	Interval           time.Duration
	ProbeTimeout       time.Duration
	DeleteInvalidFiles bool
}

type codexPoolTokenFileRecord struct {
	FilePath   string
	Identities []string
}

var (
	codexPoolHealthCheckOnce    sync.Once
	codexPoolHealthCheckRunning atomic.Bool
)

func StartCodexPoolHealthCheckTask() {
	codexPoolHealthCheckOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		gopool.Go(func() {
			logger.LogInfo(context.Background(), "codex pool health check task started")
			for {
				cfg := loadCodexPoolHealthCheckConfig()
				if !cfg.Enabled {
					time.Sleep(time.Minute)
					continue
				}
				if cfg.ChannelID <= 0 {
					logger.LogWarn(context.Background(), "codex pool health check disabled: CODEX_POOL_CHANNEL_ID is required")
					time.Sleep(time.Minute)
					continue
				}

				runCodexPoolHealthCheckOnce(cfg)
				time.Sleep(cfg.Interval)
			}
		})
	})
}

func loadCodexPoolHealthCheckConfig() codexPoolHealthCheckConfig {
	poolSetting := operation_setting.GetCodexPoolSetting()

	enabled := common.GetEnvOrDefaultBool("CODEX_POOL_HEALTHCHECK_ENABLED", false)
	if poolSetting.AutoHealthCheckEnabled {
		enabled = true
	}

	intervalSec := common.GetEnvOrDefault("CODEX_POOL_HEALTHCHECK_INTERVAL_SECONDS", 600)
	if poolSetting.AutoHealthCheckMinutes > 0 {
		intervalSec = poolSetting.AutoHealthCheckMinutes * 60
	}
	if intervalSec < 60 {
		intervalSec = 60
	}

	timeoutSec := common.GetEnvOrDefault("CODEX_POOL_HEALTHCHECK_TIMEOUT_SECONDS", 15)
	if timeoutSec < 5 {
		timeoutSec = 5
	}

	deleteInvalidFiles := common.GetEnvOrDefaultBool("CODEX_POOL_HEALTHCHECK_DELETE_INVALID_FILES", false)
	if poolSetting.DeleteInvalidTokenFiles {
		deleteInvalidFiles = true
	}

	return codexPoolHealthCheckConfig{
		Enabled:            enabled,
		ChannelID:          common.GetEnvOrDefault("CODEX_POOL_CHANNEL_ID", 0),
		TokenDir:           strings.TrimSpace(DefaultCodexPoolTokenDir()),
		Interval:           time.Duration(intervalSec) * time.Second,
		ProbeTimeout:       time.Duration(timeoutSec) * time.Second,
		DeleteInvalidFiles: deleteInvalidFiles,
	}
}

func runCodexPoolHealthCheckOnce(cfg codexPoolHealthCheckConfig) {
	if !codexPoolHealthCheckRunning.CompareAndSwap(false, true) {
		return
	}
	defer codexPoolHealthCheckRunning.Store(false)

	ctx := context.Background()
	ch, err := model.GetChannelById(cfg.ChannelID, true)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("codex pool health check: load channel failed: %v", err))
		return
	}
	if ch == nil {
		logger.LogWarn(ctx, "codex pool health check: channel not found")
		return
	}
	if ch.Type != constant.ChannelTypeCodex {
		logger.LogWarn(ctx, fmt.Sprintf("codex pool health check: channel_id=%d is not Codex", ch.Id))
		return
	}
	if ch.Status == common.ChannelStatusManuallyDisabled {
		return
	}
	if !ch.ChannelInfo.IsMultiKey {
		logger.LogWarn(ctx, fmt.Sprintf("codex pool health check: channel_id=%d is not multi-key", ch.Id))
		return
	}

	keys := ch.GetKeys()
	if len(keys) == 0 {
		logger.LogWarn(ctx, fmt.Sprintf("codex pool health check: channel_id=%d has no keys", ch.Id))
		return
	}

	client, err := NewProxyHttpClient(ch.GetSetting().Proxy)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("codex pool health check: create http client failed: %v", err))
		return
	}

	fileRecords := make([]codexPoolTokenFileRecord, 0)
	if cfg.DeleteInvalidFiles {
		fileRecords, err = collectCodexPoolTokenFileRecords(cfg.TokenDir)
		if err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("codex pool health check: scan token dir failed: %v", err))
		}
	}

	var (
		scanned       int
		healthy       int
		refreshed     int
		disabled      int
		reEnabled     int
		deletedFiles  int
		cacheReset    bool
		shouldResync  bool
	)

	for idx, rawKey := range keys {
		scanned++
		currentStatus := codexPoolKeyStatus(ch, idx)
		if currentStatus == common.ChannelStatusAutoDisabled {
			removed, deletedNow := RemoveChannelKeyPermanentlyFromPoolWithTokenDir(
				*types.NewChannelError(ch.Id, ch.Type, ch.Name, ch.ChannelInfo.IsMultiKey, strings.TrimSpace(rawKey), ch.GetAutoBan()),
				"historical auto-disabled key removed by codex pool health check",
				cfg.TokenDir,
			)
			if removed {
				disabled++
				cacheReset = true
				if deletedNow > 0 {
					deletedFiles += deletedNow
					fileRecords, _ = collectCodexPoolTokenFileRecords(cfg.TokenDir)
				}
			}
			continue
		}
		finalKey, apiErr, _, probeErr := probeCodexPoolCredential(
			ctx,
			client,
			ch.GetBaseURL(),
			strings.TrimSpace(rawKey),
			ch.GetSetting().Proxy,
			cfg.ProbeTimeout,
		)
		if probeErr != nil {
			logger.LogWarn(ctx, fmt.Sprintf("codex pool health check: channel_id=%d key_index=%d probe failed: %v", ch.Id, idx, probeErr))
			continue
		}

		if finalKey != "" && finalKey != rawKey {
			if updateErr := updateCodexPoolChannelKeyAtIndex(ch, idx, finalKey); updateErr != nil {
				logger.LogWarn(ctx, fmt.Sprintf("codex pool health check: channel_id=%d key_index=%d update refreshed key failed: %v", ch.Id, idx, updateErr))
			} else {
				keys[idx] = finalKey
				cacheReset = true
				refreshed++
				if cfg.DeleteInvalidFiles {
					updated, updateFilesErr := updateCodexPoolTokenFiles(fileRecords, rawKey, finalKey)
					if updateFilesErr != nil {
						logger.LogWarn(ctx, fmt.Sprintf("codex pool health check: channel_id=%d key_index=%d update token files failed: %v", ch.Id, idx, updateFilesErr))
					} else if updated > 0 {
						fileRecords, _ = collectCodexPoolTokenFileRecords(cfg.TokenDir)
					}
				}
			}
		}

		usingKey := keys[idx]
		if usingKey == "" {
			usingKey = rawKey
		}

		if apiErr == nil {
			healthy++
			continue
		}

		if !ShouldDisableChannel(ch.Type, apiErr) || !ch.GetAutoBan() {
			continue
		}

		reason := buildCodexPoolDisableReason(apiErr)
		if ShouldRemoveChannelKeyFromPool(*types.NewChannelError(ch.Id, ch.Type, ch.Name, ch.ChannelInfo.IsMultiKey, usingKey, ch.GetAutoBan()), apiErr) {
			removed, deletedNow := RemoveChannelKeyPermanentlyFromPoolWithTokenDir(
				*types.NewChannelError(ch.Id, ch.Type, ch.Name, ch.ChannelInfo.IsMultiKey, usingKey, ch.GetAutoBan()),
				reason,
				cfg.TokenDir,
			)
			if removed {
				disabled++
				cacheReset = true
				if deletedNow > 0 {
					deletedFiles += deletedNow
					fileRecords, _ = collectCodexPoolTokenFileRecords(cfg.TokenDir)
				}
			}
			continue
		}

		if model.UpdateChannelStatus(ch.Id, usingKey, common.ChannelStatusAutoDisabled, reason) {
			disabled++
			cacheReset = true
		}

		if cfg.DeleteInvalidFiles && shouldDeleteCodexPoolTokenFiles(apiErr) {
			deletedNow, deleteErr := deleteCodexPoolTokenFiles(fileRecords, usingKey)
			if deleteErr != nil {
				logger.LogWarn(ctx, fmt.Sprintf("codex pool health check: channel_id=%d key_index=%d delete token files failed: %v", ch.Id, idx, deleteErr))
			} else if deletedNow > 0 {
				deletedFiles += deletedNow
				shouldResync = true
				fileRecords, _ = collectCodexPoolTokenFileRecords(cfg.TokenDir)
			}
		}
	}

	if shouldResync {
		if _, err = SyncCodexChannelFromTokenDir(cfg.ChannelID, cfg.TokenDir); err != nil {
			logger.LogWarn(ctx, fmt.Sprintf("codex pool health check: resync after delete failed: %v", err))
		} else {
			cacheReset = false
		}
	}

	if cacheReset {
		model.InitChannelCache()
		ResetProxyClientCache()
	}

	logger.LogInfo(
		ctx,
		fmt.Sprintf(
			"codex pool health check: channel_id=%d scanned=%d healthy=%d refreshed=%d disabled=%d re_enabled=%d deleted_files=%d",
			ch.Id,
			scanned,
			healthy,
			refreshed,
			disabled,
			reEnabled,
			deletedFiles,
		),
	)
}

func codexPoolKeyStatus(ch *model.Channel, idx int) int {
	if ch == nil || ch.ChannelInfo.MultiKeyStatusList == nil {
		return common.ChannelStatusEnabled
	}
	if status, ok := ch.ChannelInfo.MultiKeyStatusList[idx]; ok {
		return status
	}
	return common.ChannelStatusEnabled
}

func probeCodexPoolCredential(
	ctx context.Context,
	client *http.Client,
	baseURL string,
	rawKey string,
	proxyURL string,
	timeout time.Duration,
) (finalKey string, apiErr *types.NewAPIError, refreshed bool, err error) {
	finalKey = strings.TrimSpace(rawKey)
	oauthKey, err := parseCodexOAuthKey(finalKey)
	if err != nil {
		return finalKey, nil, false, err
	}

	fetchUsage := func(accessToken string, accountID string) *types.NewAPIError {
		reqCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		statusCode, body, fetchErr := FetchCodexWhamUsage(reqCtx, client, baseURL, accessToken, accountID)
		if fetchErr != nil {
			return types.NewOpenAIError(fetchErr, types.ErrorCodeDoRequestFailed, 0)
		}
		if statusCode >= 200 && statusCode < 300 {
			return nil
		}
		return buildCodexPoolProbeAPIError(statusCode, body)
	}

	ensureCredential := func() bool {
		return strings.TrimSpace(oauthKey.AccessToken) != "" && strings.TrimSpace(oauthKey.AccountID) != ""
	}

	refreshCredential := func() (bool, error) {
		if strings.TrimSpace(oauthKey.RefreshToken) == "" {
			return false, nil
		}
		refreshCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		res, refreshErr := RefreshCodexOAuthTokenWithProxy(refreshCtx, oauthKey.RefreshToken, proxyURL)
		if refreshErr != nil {
			return false, refreshErr
		}

		oauthKey.AccessToken = strings.TrimSpace(res.AccessToken)
		oauthKey.RefreshToken = strings.TrimSpace(res.RefreshToken)
		oauthKey.LastRefresh = time.Now().Format(time.RFC3339)
		oauthKey.Expired = res.ExpiresAt.Format(time.RFC3339)
		if strings.TrimSpace(oauthKey.Type) == "" {
			oauthKey.Type = "codex"
		}
		if strings.TrimSpace(oauthKey.AccountID) == "" {
			if accountID, ok := ExtractCodexAccountIDFromJWT(oauthKey.AccessToken); ok {
				oauthKey.AccountID = accountID
			}
		}
		if strings.TrimSpace(oauthKey.Email) == "" {
			if email, ok := ExtractEmailFromJWT(oauthKey.AccessToken); ok {
				oauthKey.Email = email
			}
		}
		encoded, marshalErr := common.Marshal(oauthKey)
		if marshalErr != nil {
			return false, marshalErr
		}
		finalKey = strings.TrimSpace(string(encoded))
		return true, nil
	}

	if !ensureCredential() {
		refreshed, err = refreshCredential()
		if err != nil {
			return finalKey, nil, false, err
		}
		if !ensureCredential() {
			return finalKey, nil, refreshed, fmt.Errorf("codex oauth key missing access_token/account_id")
		}
	}

	apiErr = fetchUsage(oauthKey.AccessToken, oauthKey.AccountID)
	if apiErr == nil {
		return finalKey, nil, refreshed, nil
	}

	if apiErr.StatusCode != 401 && apiErr.StatusCode != 403 {
		return finalKey, apiErr, refreshed, nil
	}

	refreshedThisRound, refreshErr := refreshCredential()
	if refreshErr != nil || !refreshedThisRound {
		return finalKey, apiErr, refreshed, nil
	}
	refreshed = true

	retryErr := fetchUsage(oauthKey.AccessToken, oauthKey.AccountID)
	return finalKey, retryErr, refreshed, nil
}

func buildCodexPoolProbeAPIError(statusCode int, body []byte) *types.NewAPIError {
	var errResp dto.GeneralErrorResponse
	if err := common.Unmarshal(body, &errResp); err == nil {
		if oaiErr := errResp.TryToOpenAIError(); oaiErr != nil {
			return types.WithOpenAIError(*oaiErr, statusCode)
		}
		message := strings.TrimSpace(errResp.ToMessage())
		if message != "" {
			return types.NewOpenAIError(fmt.Errorf("%s", message), types.ErrorCodeBadResponseStatusCode, statusCode)
		}
	}
	if len(body) == 0 {
		return types.NewOpenAIError(fmt.Errorf("bad response status code %d", statusCode), types.ErrorCodeBadResponseStatusCode, statusCode)
	}
	return types.NewOpenAIError(fmt.Errorf("bad response status code %d, body: %s", statusCode, string(body)), types.ErrorCodeBadResponseStatusCode, statusCode)
}

func buildCodexPoolDisableReason(apiErr *types.NewAPIError) string {
	if apiErr == nil {
		return "status_code=0, unknown_error"
	}
	oaiErr := apiErr.ToOpenAIError()
	code := strings.TrimSpace(fmt.Sprintf("%v", oaiErr.Code))
	if code != "" && code != "<nil>" {
		if apiErr.StatusCode > 0 {
			return fmt.Sprintf("status_code=%d, %s", apiErr.StatusCode, code)
		}
		return code
	}
	return apiErr.ErrorWithStatusCode()
}

func shouldDeleteCodexPoolTokenFiles(apiErr *types.NewAPIError) bool {
	if apiErr == nil {
		return false
	}
	oaiErr := apiErr.ToOpenAIError()
	code := strings.TrimSpace(fmt.Sprintf("%v", oaiErr.Code))
	switch code {
	case "token_invalidated", "account_deactivated", "invalid_api_key":
		return true
	default:
		return false
	}
}

func collectCodexPoolTokenFileRecords(tokenDir string) ([]codexPoolTokenFileRecord, error) {
	dir := strings.TrimSpace(tokenDir)
	if dir == "" {
		dir = DefaultCodexPoolTokenDir()
	}
	absDir, err := filepath.Abs(dir)
	if err == nil {
		dir = absDir
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	records := make([]codexPoolTokenFileRecord, 0, len(entries))
	for _, entry := range entries {
		if entry == nil || entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		fullPath := filepath.Join(dir, entry.Name())
		raw, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			continue
		}
		identities := buildCodexPoolStatusIdentities(string(raw))
		if len(identities) == 0 {
			continue
		}
		records = append(records, codexPoolTokenFileRecord{
			FilePath:   fullPath,
			Identities: identities,
		})
	}
	return records, nil
}

func matchCodexPoolTokenFilePaths(records []codexPoolTokenFileRecord, rawKey string) []string {
	targetIdentities := buildCodexPoolStatusIdentities(rawKey)
	if len(targetIdentities) == 0 || len(records) == 0 {
		return nil
	}

	targetSet := make(map[string]struct{}, len(targetIdentities))
	for _, identity := range targetIdentities {
		targetSet[identity] = struct{}{}
	}

	paths := make([]string, 0)
	seen := make(map[string]struct{})
	for _, record := range records {
		if record.FilePath == "" {
			continue
		}
		for _, identity := range record.Identities {
			if _, ok := targetSet[identity]; !ok {
				continue
			}
			if _, exists := seen[record.FilePath]; !exists {
				seen[record.FilePath] = struct{}{}
				paths = append(paths, record.FilePath)
			}
			break
		}
	}
	return paths
}

func updateCodexPoolTokenFiles(records []codexPoolTokenFileRecord, rawKey string, newRawKey string) (int, error) {
	paths := matchCodexPoolTokenFilePaths(records, rawKey)
	if len(paths) == 0 {
		return 0, nil
	}

	data := []byte(strings.TrimSpace(newRawKey))
	updated := 0
	for _, path := range paths {
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func deleteCodexPoolTokenFiles(records []codexPoolTokenFileRecord, rawKey string) (int, error) {
	paths := matchCodexPoolTokenFilePaths(records, rawKey)
	deleted := 0
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func DeleteCodexPoolTokenFilesByKey(tokenDir string, rawKey string) (int, error) {
	records, err := collectCodexPoolTokenFileRecords(tokenDir)
	if err != nil {
		return 0, err
	}
	return deleteCodexPoolTokenFiles(records, rawKey)
}

func DeleteAllCodexPoolTokenFiles(tokenDir string) (int, error) {
	records, err := collectCodexPoolTokenFileRecords(tokenDir)
	if err != nil {
		return 0, err
	}
	deleted := 0
	for _, record := range records {
		path := strings.TrimSpace(record.FilePath)
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return deleted, err
		}
		deleted++
	}
	return deleted, nil
}

func updateCodexPoolChannelKeyAtIndex(ch *model.Channel, keyIndex int, rawKey string) error {
	if ch == nil {
		return fmt.Errorf("channel is nil")
	}
	keys := ch.GetKeys()
	if keyIndex < 0 || keyIndex >= len(keys) {
		return fmt.Errorf("key index out of range")
	}
	keys[keyIndex] = strings.TrimSpace(rawKey)
	joined := strings.Join(keys, "\n")
	if err := model.DB.Model(&model.Channel{}).Where("id = ?", ch.Id).Update("key", joined).Error; err != nil {
		return err
	}
	ch.Key = joined
	model.InitChannelCache()
	ResetProxyClientCache()
	return nil
}
