package service

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

type CodexPoolSyncResult struct {
	ChannelID    int    `json:"channel_id"`
	TokenDir     string `json:"token_dir"`
	FilesTotal   int    `json:"files_total"`
	FilesLoaded  int    `json:"files_loaded"`
	FilesInvalid int    `json:"files_invalid"`
	TotalKeys    int    `json:"total_keys"`
	EnabledKeys  int    `json:"enabled_keys"`
	DisabledKeys int    `json:"disabled_keys"`
	Updated      bool   `json:"updated"`
}

type codexPoolCollectStats struct {
	FilesTotal   int
	FilesLoaded  int
	FilesInvalid int
}

type codexPoolTokenCandidate struct {
	KeyString      string
	Email          string
	AccountID      string
	DedupIdentity  string
	LastRefreshRaw string
	LastRefreshAt  time.Time
	FileModTime    time.Time
}

type codexPoolDisabledState struct {
	Status int
	Time   int64
	Reason string
}

const codexPoolRejectedIdentitiesKey = "codex_pool_rejected_identities"

func DefaultCodexPoolTokenDir() string {
	return common.GetEnvOrDefaultString("CODEX_POOL_TOKEN_DIR", "chatgpt_register_v2_by_AI/tokens")
}

func shouldDeleteSyncedCodexPoolTokenFiles() bool {
	if common.GetEnvOrDefaultBool("CODEX_POOL_SYNC_DELETE_IMPORTED_FILES", true) {
		return true
	}
	poolSetting := operation_setting.GetCodexPoolSetting()
	return poolSetting != nil && poolSetting.DeleteSyncedTokenFiles
}

func extractCodexPoolRejectedIdentities(value interface{}) []string {
	switch v := value.(type) {
	case nil:
		return nil
	case []string:
		result := make([]string, 0, len(v))
		for _, item := range v {
			item = strings.TrimSpace(item)
			if item != "" {
				result = append(result, item)
			}
		}
		return result
	case []interface{}:
		result := make([]string, 0, len(v))
		for _, item := range v {
			text, ok := item.(string)
			if !ok {
				continue
			}
			text = strings.TrimSpace(text)
			if text != "" {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func getCodexPoolRejectedIdentitySet(ch *model.Channel) map[string]struct{} {
	if ch == nil {
		return nil
	}

	info := ch.GetOtherInfo()
	identities := extractCodexPoolRejectedIdentities(info[codexPoolRejectedIdentitiesKey])
	if len(identities) == 0 {
		return nil
	}

	result := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		identity = strings.TrimSpace(identity)
		if identity == "" {
			continue
		}
		result[identity] = struct{}{}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func filterCodexPoolKeysByRejectedIdentities(keys []string, rejected map[string]struct{}) ([]string, int) {
	if len(keys) == 0 || len(rejected) == 0 {
		return keys, 0
	}

	filtered := make([]string, 0, len(keys))
	skipped := 0
	for _, rawKey := range keys {
		blocked := false
		for _, identity := range buildCodexPoolStatusIdentities(rawKey) {
			if _, ok := rejected[identity]; ok {
				blocked = true
				break
			}
		}
		if blocked {
			skipped++
			continue
		}
		filtered = append(filtered, rawKey)
	}
	return filtered, skipped
}

func isCodexPoolKeyRejected(rawKey string, rejected map[string]struct{}) bool {
	if len(rejected) == 0 {
		return false
	}
	for _, identity := range buildCodexPoolStatusIdentities(rawKey) {
		if _, ok := rejected[identity]; ok {
			return true
		}
	}
	return false
}

func buildCodexPoolIdentityIndex(keys []string) map[string]int {
	index := make(map[string]int)
	for i, rawKey := range keys {
		for _, identity := range buildCodexPoolStatusIdentities(rawKey) {
			if identity == "" {
				continue
			}
			if _, exists := index[identity]; !exists {
				index[identity] = i
			}
		}
	}
	return index
}

func mergeCodexPoolKeys(existingKeys []string, importedKeys []string, rejected map[string]struct{}) ([]string, int) {
	merged := make([]string, 0, len(existingKeys)+len(importedKeys))
	for _, rawKey := range existingKeys {
		rawKey = strings.TrimSpace(rawKey)
		if rawKey == "" || isCodexPoolKeyRejected(rawKey, rejected) {
			continue
		}
		merged = append(merged, rawKey)
	}

	importedCount := 0
	for _, rawKey := range importedKeys {
		rawKey = strings.TrimSpace(rawKey)
		if rawKey == "" || isCodexPoolKeyRejected(rawKey, rejected) {
			continue
		}

		identityIndex := buildCodexPoolIdentityIndex(merged)
		replaceIdx := -1
		for _, identity := range buildCodexPoolStatusIdentities(rawKey) {
			if idx, ok := identityIndex[identity]; ok {
				replaceIdx = idx
				break
			}
		}

		if replaceIdx >= 0 {
			merged[replaceIdx] = rawKey
		} else {
			merged = append(merged, rawKey)
		}
		importedCount++
	}

	return merged, importedCount
}

func BuildCodexPoolSyncResultFromChannel(channelID int, tokenDir string) (*CodexPoolSyncResult, error) {
	if channelID <= 0 {
		return nil, fmt.Errorf("invalid channel id")
	}

	ch, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, fmt.Errorf("channel not found")
	}
	if ch.Type != constant.ChannelTypeCodex {
		return nil, fmt.Errorf("channel type is not Codex")
	}

	keys := ch.GetKeys()
	disabledCount := len(ch.ChannelInfo.MultiKeyStatusList)
	if disabledCount > len(keys) {
		disabledCount = len(keys)
	}

	return &CodexPoolSyncResult{
		ChannelID:    channelID,
		TokenDir:     strings.TrimSpace(tokenDir),
		FilesTotal:   0,
		FilesLoaded:  0,
		FilesInvalid: 0,
		TotalKeys:    len(keys),
		EnabledKeys:  len(keys) - disabledCount,
		DisabledKeys: disabledCount,
		Updated:      false,
	}, nil
}

func SyncCodexChannelFromTokenDir(channelID int, tokenDir string) (*CodexPoolSyncResult, error) {
	if channelID <= 0 {
		return nil, fmt.Errorf("invalid channel id")
	}

	dir := strings.TrimSpace(tokenDir)
	if dir == "" {
		dir = DefaultCodexPoolTokenDir()
	}
	absDir, err := filepath.Abs(dir)
	if err == nil {
		dir = absDir
	}

	keys, stats, err := collectCodexPoolKeysFromTokenDir(dir)
	if err != nil {
		return nil, err
	}

	return syncCodexChannelPool(channelID, keys, dir, stats)
}

func ImportCodexChannelOAuthKey(channelID int, oauthKey *CodexOAuthKey) (*CodexPoolSyncResult, error) {
	if channelID <= 0 {
		return nil, fmt.Errorf("invalid channel id")
	}
	normalized, err := normalizeImportedCodexOAuthKey(oauthKey)
	if err != nil {
		return nil, err
	}

	encoded, err := common.Marshal(normalized)
	if err != nil {
		return nil, err
	}

	return syncCodexChannelPool(channelID, []string{strings.TrimSpace(string(encoded))}, "", codexPoolCollectStats{
		FilesTotal:  1,
		FilesLoaded: 1,
	})
}

func normalizeImportedCodexOAuthKey(oauthKey *CodexOAuthKey) (*CodexOAuthKey, error) {
	if oauthKey == nil {
		return nil, fmt.Errorf("codex oauth key is required")
	}

	normalized := *oauthKey
	normalized.Type = strings.TrimSpace(strings.ToLower(normalized.Type))
	normalized.Email = strings.TrimSpace(strings.ToLower(normalized.Email))
	normalized.AccountID = strings.TrimSpace(normalized.AccountID)
	normalized.AccessToken = strings.TrimSpace(normalized.AccessToken)
	normalized.RefreshToken = strings.TrimSpace(normalized.RefreshToken)
	normalized.IDToken = strings.TrimSpace(normalized.IDToken)
	normalized.LastRefresh = strings.TrimSpace(normalized.LastRefresh)
	normalized.Expired = strings.TrimSpace(normalized.Expired)

	if normalized.Type == "" {
		normalized.Type = "codex"
	}
	if normalized.Type != "codex" {
		return nil, fmt.Errorf("unsupported auth file type: %s", normalized.Type)
	}
	if normalized.AccessToken == "" {
		return nil, fmt.Errorf("access_token is required")
	}
	if normalized.AccountID == "" {
		if accountID, ok := ExtractCodexAccountIDFromJWT(normalized.AccessToken); ok {
			normalized.AccountID = strings.TrimSpace(accountID)
		}
	}
	if normalized.AccountID == "" {
		return nil, fmt.Errorf("account_id is required")
	}
	if normalized.Email == "" {
		if email, ok := ExtractEmailFromJWT(normalized.AccessToken); ok {
			normalized.Email = strings.TrimSpace(strings.ToLower(email))
		}
	}
	if normalized.LastRefresh == "" {
		normalized.LastRefresh = time.Now().Format(time.RFC3339)
	}

	return &normalized, nil
}

func syncCodexChannelPool(channelID int, importedKeys []string, tokenDir string, stats codexPoolCollectStats) (*CodexPoolSyncResult, error) {
	if len(importedKeys) == 0 {
		if tokenDir == "" {
			return nil, fmt.Errorf("no valid codex oauth keys found")
		}
		return nil, fmt.Errorf("no valid codex oauth keys found in %s", tokenDir)
	}

	ch, err := model.GetChannelById(channelID, true)
	if err != nil {
		return nil, err
	}
	if ch == nil {
		return nil, fmt.Errorf("channel not found")
	}
	if ch.Type != constant.ChannelTypeCodex {
		return nil, fmt.Errorf("channel type is not Codex")
	}

	rejectedIdentities := getCodexPoolRejectedIdentitySet(ch)
	filteredKeys, skippedRejected := filterCodexPoolKeysByRejectedIdentities(importedKeys, rejectedIdentities)
	if skippedRejected > 0 {
		common.SysLog(fmt.Sprintf("codex pool sync: filtered %d rejected keys for channel_id=%d", skippedRejected, channelID))
	}
	if len(filteredKeys) == 0 {
		if tokenDir == "" {
			return nil, fmt.Errorf("no importable codex oauth keys found")
		}
		return nil, fmt.Errorf("no importable codex oauth keys found in %s", tokenDir)
	}

	previousInfo := ch.ChannelInfo
	prevIsMultiKey := previousInfo.IsMultiKey
	prevMode := previousInfo.MultiKeyMode
	prevStatus := previousInfo.MultiKeyStatusList
	prevTime := previousInfo.MultiKeyDisabledTime
	prevReason := previousInfo.MultiKeyDisabledReason
	prevChannelStatus := ch.Status
	prevOtherInfo := ch.OtherInfo
	prevKeys := ch.GetKeys()
	prevKeyString := strings.TrimSpace(ch.Key)

	keys, importedCount := mergeCodexPoolKeys(prevKeys, filteredKeys, rejectedIdentities)
	if importedCount > 0 {
		common.SysLog(fmt.Sprintf("codex pool sync: merged %d imported keys into channel_id=%d", importedCount, channelID))
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no active codex oauth keys available for channel %d after merge", channelID)
	}

	newStatus, newTime, newReason := remapCodexPoolKeyStatus(prevKeys, prevStatus, prevTime, prevReason, keys)

	ch.Key = strings.Join(keys, "\n")
	ch.ChannelInfo.IsMultiKey = true
	if ch.ChannelInfo.MultiKeyMode == "" {
		ch.ChannelInfo.MultiKeyMode = constant.MultiKeyModeRandom
	}
	ch.ChannelInfo.MultiKeySize = len(keys)
	if len(newStatus) == 0 {
		ch.ChannelInfo.MultiKeyStatusList = nil
	} else {
		ch.ChannelInfo.MultiKeyStatusList = newStatus
	}
	if len(newTime) == 0 {
		ch.ChannelInfo.MultiKeyDisabledTime = nil
	} else {
		ch.ChannelInfo.MultiKeyDisabledTime = newTime
	}
	if len(newReason) == 0 {
		ch.ChannelInfo.MultiKeyDisabledReason = nil
	} else {
		ch.ChannelInfo.MultiKeyDisabledReason = newReason
	}
	if ch.Status != common.ChannelStatusManuallyDisabled {
		if len(keys) > 0 && len(newStatus) >= len(keys) {
			ch.Status = common.ChannelStatusAutoDisabled
			info := ch.GetOtherInfo()
			info["status_reason"] = "All keys are disabled"
			info["status_time"] = common.GetTimestamp()
			ch.SetOtherInfo(info)
		} else {
			ch.Status = common.ChannelStatusEnabled
			info := ch.GetOtherInfo()
			delete(info, "status_reason")
			delete(info, "status_time")
			ch.SetOtherInfo(info)
		}
	}

	updated := shouldUpdateCodexPoolChannel(
		prevKeyString,
		strings.TrimSpace(ch.Key),
		prevIsMultiKey,
		ch.ChannelInfo.IsMultiKey,
		prevMode,
		ch.ChannelInfo.MultiKeyMode,
		prevStatus,
		ch.ChannelInfo.MultiKeyStatusList,
		prevTime,
		ch.ChannelInfo.MultiKeyDisabledTime,
		prevReason,
		ch.ChannelInfo.MultiKeyDisabledReason,
		prevChannelStatus,
		ch.Status,
		prevOtherInfo,
		ch.OtherInfo,
	)

	if updated {
		if err = ch.Update(); err != nil {
			return nil, err
		}
		model.InitChannelCache()
		ResetProxyClientCache()
	}

	disabledCount := len(ch.ChannelInfo.MultiKeyStatusList)
	if disabledCount > len(keys) {
		disabledCount = len(keys)
	}
	result := &CodexPoolSyncResult{
		ChannelID:    channelID,
		TokenDir:     strings.TrimSpace(tokenDir),
		FilesTotal:   stats.FilesTotal,
		FilesLoaded:  stats.FilesLoaded,
		FilesInvalid: stats.FilesInvalid,
		TotalKeys:    len(keys),
		EnabledKeys:  len(keys) - disabledCount,
		DisabledKeys: disabledCount,
		Updated:      updated,
	}

	if tokenDir != "" && shouldDeleteSyncedCodexPoolTokenFiles() {
		if _, deleteErr := DeleteAllCodexPoolTokenFiles(tokenDir); deleteErr != nil {
			return nil, fmt.Errorf("sync succeeded but delete synced token files failed: %w", deleteErr)
		}
	}
	return result, nil
}

func collectCodexPoolKeysFromTokenDir(tokenDir string) ([]string, codexPoolCollectStats, error) {
	stats := codexPoolCollectStats{}
	entries, err := os.ReadDir(tokenDir)
	if err != nil {
		return nil, stats, fmt.Errorf("failed to read token dir %s: %w", tokenDir, err)
	}

	candidateMap := make(map[string]codexPoolTokenCandidate)

	for _, entry := range entries {
		if entry == nil || entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		stats.FilesTotal++

		fullPath := filepath.Join(tokenDir, name)
		raw, readErr := os.ReadFile(fullPath)
		if readErr != nil {
			stats.FilesInvalid++
			continue
		}

		var key CodexOAuthKey
		if err = common.Unmarshal(raw, &key); err != nil {
			stats.FilesInvalid++
			continue
		}

		key.AccessToken = strings.TrimSpace(key.AccessToken)
		key.RefreshToken = strings.TrimSpace(key.RefreshToken)
		key.AccountID = strings.TrimSpace(key.AccountID)
		key.Email = strings.TrimSpace(strings.ToLower(key.Email))
		key.IDToken = strings.TrimSpace(key.IDToken)
		key.LastRefresh = strings.TrimSpace(key.LastRefresh)
		key.Expired = strings.TrimSpace(key.Expired)
		if key.Type == "" {
			key.Type = "codex"
		}

		if key.AccessToken == "" || key.AccountID == "" {
			stats.FilesInvalid++
			continue
		}

		encoded, marshalErr := common.Marshal(key)
		if marshalErr != nil {
			stats.FilesInvalid++
			continue
		}

		fileInfo, infoErr := entry.Info()
		fileModTime := time.Time{}
		if infoErr == nil {
			fileModTime = fileInfo.ModTime()
		}
		lastRefreshAt := parsePoolTime(key.LastRefresh)
		dedupIdentity := buildCodexPoolDedupIdentity(key)
		candidate := codexPoolTokenCandidate{
			KeyString:      strings.TrimSpace(string(encoded)),
			Email:          key.Email,
			AccountID:      key.AccountID,
			DedupIdentity:  dedupIdentity,
			LastRefreshRaw: key.LastRefresh,
			LastRefreshAt:  lastRefreshAt,
			FileModTime:    fileModTime,
		}

		existing, exists := candidateMap[dedupIdentity]
		if !exists || shouldReplaceCodexPoolCandidate(existing, candidate) {
			candidateMap[dedupIdentity] = candidate
		}
		stats.FilesLoaded++
	}

	if len(candidateMap) == 0 {
		return nil, stats, nil
	}

	candidates := make([]codexPoolTokenCandidate, 0, len(candidateMap))
	for _, v := range candidateMap {
		candidates = append(candidates, v)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Email != candidates[j].Email {
			return candidates[i].Email < candidates[j].Email
		}
		if candidates[i].AccountID != candidates[j].AccountID {
			return candidates[i].AccountID < candidates[j].AccountID
		}
		if candidates[i].LastRefreshRaw != candidates[j].LastRefreshRaw {
			return candidates[i].LastRefreshRaw > candidates[j].LastRefreshRaw
		}
		return candidates[i].KeyString < candidates[j].KeyString
	})

	keys := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.KeyString == "" {
			continue
		}
		keys = append(keys, candidate.KeyString)
	}
	return keys, stats, nil
}

func shouldReplaceCodexPoolCandidate(oldCandidate codexPoolTokenCandidate, newCandidate codexPoolTokenCandidate) bool {
	if !newCandidate.LastRefreshAt.IsZero() && oldCandidate.LastRefreshAt.IsZero() {
		return true
	}
	if !newCandidate.LastRefreshAt.IsZero() && newCandidate.LastRefreshAt.After(oldCandidate.LastRefreshAt) {
		return true
	}
	if newCandidate.LastRefreshAt.Equal(oldCandidate.LastRefreshAt) && newCandidate.FileModTime.After(oldCandidate.FileModTime) {
		return true
	}
	return false
}

func buildCodexPoolDedupIdentity(key CodexOAuthKey) string {
	if key.AccountID != "" {
		return "account:" + key.AccountID
	}
	if key.RefreshToken != "" {
		return "refresh:" + key.RefreshToken
	}
	if key.Email != "" {
		return "email:" + key.Email
	}
	return "access:" + key.AccessToken
}

func parsePoolTime(raw string) time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

func remapCodexPoolKeyStatus(
	oldKeys []string,
	oldStatus map[int]int,
	oldDisabledTime map[int]int64,
	oldDisabledReason map[int]string,
	newKeys []string,
) (map[int]int, map[int]int64, map[int]string) {
	oldStateByIdentity := make(map[string]codexPoolDisabledState)

	for idx, rawKey := range oldKeys {
		status := common.ChannelStatusEnabled
		if oldStatus != nil {
			if s, ok := oldStatus[idx]; ok {
				status = s
			}
		}
		if status == common.ChannelStatusEnabled {
			continue
		}

		state := codexPoolDisabledState{Status: status}
		if oldDisabledTime != nil {
			if v, ok := oldDisabledTime[idx]; ok {
				state.Time = v
			}
		}
		if oldDisabledReason != nil {
			if v, ok := oldDisabledReason[idx]; ok {
				state.Reason = v
			}
		}

		for _, identity := range buildCodexPoolStatusIdentities(rawKey) {
			if identity == "" {
				continue
			}
			if _, exists := oldStateByIdentity[identity]; !exists {
				oldStateByIdentity[identity] = state
			}
		}
	}

	newStatus := make(map[int]int)
	newTime := make(map[int]int64)
	newReason := make(map[int]string)
	for idx, rawKey := range newKeys {
		state, ok := matchCodexPoolDisabledState(oldStateByIdentity, rawKey)
		if !ok || state.Status == common.ChannelStatusEnabled {
			continue
		}
		newStatus[idx] = state.Status
		if state.Time > 0 {
			newTime[idx] = state.Time
		}
		if state.Reason != "" {
			newReason[idx] = state.Reason
		}
	}

	return newStatus, newTime, newReason
}

func buildCodexPoolStatusIdentities(rawKey string) []string {
	trimmed := strings.TrimSpace(rawKey)
	if trimmed == "" {
		return nil
	}

	identities := make([]string, 0, 5)
	addIdentity := func(identity string) {
		identity = strings.TrimSpace(identity)
		if identity == "" {
			return
		}
		for _, existing := range identities {
			if existing == identity {
				return
			}
		}
		identities = append(identities, identity)
	}

	if oauthKey, err := parseCodexOAuthKey(trimmed); err == nil && oauthKey != nil {
		addIdentity("account:" + strings.TrimSpace(oauthKey.AccountID))
		addIdentity("refresh:" + strings.TrimSpace(oauthKey.RefreshToken))
		addIdentity("email:" + strings.TrimSpace(strings.ToLower(oauthKey.Email)))
		addIdentity("access:" + strings.TrimSpace(oauthKey.AccessToken))
	}
	addIdentity("raw:" + trimmed)
	return identities
}

func matchCodexPoolDisabledState(states map[string]codexPoolDisabledState, rawKey string) (codexPoolDisabledState, bool) {
	for _, identity := range buildCodexPoolStatusIdentities(rawKey) {
		if state, ok := states[identity]; ok {
			return state, true
		}
	}
	return codexPoolDisabledState{}, false
}

func shouldUpdateCodexPoolChannel(
	oldKey string,
	newKey string,
	oldIsMultiKey bool,
	newIsMultiKey bool,
	oldMode constant.MultiKeyMode,
	newMode constant.MultiKeyMode,
	oldStatus map[int]int,
	newStatus map[int]int,
	oldTime map[int]int64,
	newTime map[int]int64,
	oldReason map[int]string,
	newReason map[int]string,
	oldChannelStatus int,
	newChannelStatus int,
	oldOtherInfo string,
	newOtherInfo string,
) bool {
	if oldKey != newKey {
		return true
	}
	if oldIsMultiKey != newIsMultiKey {
		return true
	}
	if oldMode != newMode {
		return true
	}
	if !reflect.DeepEqual(oldStatus, newStatus) {
		return true
	}
	if !reflect.DeepEqual(oldTime, newTime) {
		return true
	}
	if !reflect.DeepEqual(oldReason, newReason) {
		return true
	}
	if oldChannelStatus != newChannelStatus {
		return true
	}
	return oldOtherInfo != newOtherInfo
}
