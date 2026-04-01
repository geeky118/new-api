package service

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
)

func formatNotifyType(channelId int, status int) string {
	return fmt.Sprintf("%s_%d_%d", dto.NotifyTypeChannelUpdate, channelId, status)
}

func DisableChannel(channelError types.ChannelError, reason string) bool {
	common.SysLog(fmt.Sprintf("通道「%s」（#%d）发生错误，准备禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, reason))

	if !channelError.AutoBan {
		common.SysLog(fmt.Sprintf("通道「%s」（#%d）未启用自动禁用功能，跳过禁用操作", channelError.ChannelName, channelError.ChannelId))
		return false
	}

	success := model.UpdateChannelStatus(channelError.ChannelId, channelError.UsingKey, common.ChannelStatusAutoDisabled, reason)
	if success {
		subject := fmt.Sprintf("通道「%s」（#%d）已被禁用", channelError.ChannelName, channelError.ChannelId)
		content := fmt.Sprintf("通道「%s」（#%d）已被禁用，原因：%s", channelError.ChannelName, channelError.ChannelId, reason)
		NotifyRootUser(formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), subject, content)
	}
	return success
}

func RemoveChannelKeyFromPool(channelError types.ChannelError, reason string) bool {
	if !channelError.AutoBan || !channelError.IsMultiKey || channelError.UsingKey == "" {
		return false
	}

	common.SysLog(fmt.Sprintf("通道「%s」（#%d）命中 usage limit，准备将当前 key 从号池剔除，原因：%s", channelError.ChannelName, channelError.ChannelId, reason))

	success := model.RemoveChannelKey(channelError.ChannelId, channelError.UsingKey, reason)
	if success {
		subject := fmt.Sprintf("通道「%s」（#%d）账号已从号池剔除", channelError.ChannelName, channelError.ChannelId)
		content := fmt.Sprintf("通道「%s」（#%d）账号已从号池剔除，原因：%s", channelError.ChannelName, channelError.ChannelId, reason)
		NotifyRootUser(formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), subject, content)
		if channelError.ChannelType == constant.ChannelTypeCodex {
			deleted, err := DeleteCodexPoolTokenFilesByKey(DefaultCodexPoolTokenDir(), channelError.UsingKey)
			if err != nil {
				common.SysLog(fmt.Sprintf("通道「%s」（#%d）剔除账号后删除 tokens 文件失败: %v", channelError.ChannelName, channelError.ChannelId, err))
			} else if deleted > 0 {
				common.SysLog(fmt.Sprintf("通道「%s」（#%d）剔除账号后已删除 %d 个 tokens 文件", channelError.ChannelName, channelError.ChannelId, deleted))
			}
		}
	}
	return success
}

func EnableChannel(channelId int, usingKey string, channelName string) {
	success := model.UpdateChannelStatus(channelId, usingKey, common.ChannelStatusEnabled, "")
	if success {
		subject := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		content := fmt.Sprintf("通道「%s」（#%d）已被启用", channelName, channelId)
		NotifyRootUser(formatNotifyType(channelId, common.ChannelStatusEnabled), subject, content)
	}
}

func ShouldDisableChannel(channelType int, err *types.NewAPIError) bool {
	if !common.AutomaticDisableChannelEnabled {
		return false
	}
	if err == nil {
		return false
	}
	if types.IsChannelError(err) {
		return true
	}
	if types.IsSkipRetryError(err) {
		return false
	}
	if operation_setting.ShouldDisableByStatusCode(err.StatusCode) {
		return true
	}
	if err.StatusCode == http.StatusForbidden {
		switch channelType {
		case constant.ChannelTypeGemini:
			return true
		}
	}

	oaiErr := err.ToOpenAIError()
	code := strings.ToLower(fmt.Sprintf("%v", oaiErr.Code))
	switch code {
	case "invalid_api_key":
		return true
	case "account_deactivated":
		return true
	case "billing_not_active":
		return true
	case "token_invalidated":
		return true
	case "pre_consume_token_quota_failed":
		return true
	case "arrearage":
		return true
	case "usage_limit_reached":
		return true
	}

	switch strings.ToLower(oaiErr.Type) {
	case "insufficient_quota":
		return true
	case "insufficient_user_quota":
		return true
	case "authentication_error":
		return true
	case "permission_error":
		return true
	case "forbidden":
		return true
	case "usage_limit_reached":
		return true
	}

	lowerMessage := strings.ToLower(err.Error())
	if strings.Contains(lowerMessage, "you've hit your usage limit") || strings.Contains(lowerMessage, "the usage limit has been reached") {
		return true
	}

	search, _ := AcSearch(lowerMessage, operation_setting.AutomaticDisableKeywords, true)
	return search
}

func IsUsageLimitError(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}

	oaiErr := err.ToOpenAIError()
	code := strings.ToLower(fmt.Sprintf("%v", oaiErr.Code))
	if code == "usage_limit_reached" {
		return true
	}
	if strings.ToLower(oaiErr.Type) == "usage_limit_reached" {
		return true
	}

	lowerMessage := strings.ToLower(err.Error())
	return strings.Contains(lowerMessage, "you've hit your usage limit") || strings.Contains(lowerMessage, "the usage limit has been reached")
}

func ShouldEnableChannel(newAPIError *types.NewAPIError, status int) bool {
	if !common.AutomaticEnableChannelEnabled {
		return false
	}
	if newAPIError != nil {
		return false
	}
	if status != common.ChannelStatusAutoDisabled {
		return false
	}
	return true
}

func ShouldRemoveChannelKeyFromPool(channelError types.ChannelError, err *types.NewAPIError) bool {
	if !channelError.AutoBan || !channelError.IsMultiKey || channelError.UsingKey == "" {
		return false
	}
	if channelError.ChannelType != constant.ChannelTypeCodex {
		return false
	}
	return ShouldDisableChannel(channelError.ChannelType, err)
}

func RemoveChannelKeyPermanentlyFromPool(channelError types.ChannelError, reason string) bool {
	success, _ := RemoveChannelKeyPermanentlyFromPoolWithTokenDir(channelError, reason, "")
	return success
}

func RemoveChannelKeyPermanentlyFromPoolWithTokenDir(channelError types.ChannelError, reason string, tokenDir string) (bool, int) {
	if !channelError.AutoBan || !channelError.IsMultiKey || channelError.UsingKey == "" {
		return false, 0
	}

	success := model.RemoveChannelKey(channelError.ChannelId, channelError.UsingKey, reason)
	if !success {
		return false, 0
	}

	if channelError.ChannelType == constant.ChannelTypeCodex {
		if err := appendCodexPoolRejectedIdentities(channelError.ChannelId, channelError.UsingKey); err != nil {
			common.SysLog(fmt.Sprintf("failed to persist removed codex pool identities: channel_id=%d, error=%v", channelError.ChannelId, err))
		}
	}

	subject := fmt.Sprintf("閫氶亾銆?s銆嶏紙#%d锛夎处鍙峰凡浠庡彿姹犲墧闄?, channelError.ChannelName, channelError.ChannelId)
	content := fmt.Sprintf("閫氶亾銆?s銆嶏紙#%d锛夎处鍙峰凡浠庡彿姹犲墧闄わ紝鍘熷洜锛?s", channelError.ChannelName, channelError.ChannelId, reason)
	NotifyRootUser(formatNotifyType(channelError.ChannelId, common.ChannelStatusAutoDisabled), subject, content)

	deleted := 0
	if channelError.ChannelType == constant.ChannelTypeCodex {
		dir := strings.TrimSpace(tokenDir)
		if dir == "" {
			dir = DefaultCodexPoolTokenDir()
		}
		var err error
		deleted, err = DeleteCodexPoolTokenFilesByKey(dir, channelError.UsingKey)
		if err != nil {
			common.SysLog(fmt.Sprintf("failed to delete codex pool token files after permanent removal: channel_id=%d, error=%v", channelError.ChannelId, err))
		}
	}

	return true, deleted
}

func appendCodexPoolRejectedIdentities(channelID int, rawKey string) error {
	identities := buildCodexPoolStatusIdentities(rawKey)
	if len(identities) == 0 {
		return nil
	}

	pollingLock := model.GetChannelPollingLock(channelID)
	pollingLock.Lock()
	defer pollingLock.Unlock()

	ch, err := model.GetChannelById(channelID, true)
	if err != nil {
		return err
	}
	if ch == nil {
		return fmt.Errorf("channel not found")
	}

	info := ch.GetOtherInfo()
	merged := mergeCodexPoolRejectedIdentities(info[codexPoolRejectedIdentitiesKey], identities)
	if len(merged) == 0 {
		return nil
	}

	info[codexPoolRejectedIdentitiesKey] = merged
	ch.SetOtherInfo(info)
	return model.DB.Model(&model.Channel{}).Where("id = ?", channelID).Update("other_info", ch.OtherInfo).Error
}

func mergeCodexPoolRejectedIdentities(existingValue interface{}, additions []string) []string {
	mergedSet := make(map[string]struct{})
	for _, identity := range extractCodexPoolRejectedIdentities(existingValue) {
		if identity == "" {
			continue
		}
		mergedSet[identity] = struct{}{}
	}
	for _, identity := range additions {
		identity = strings.TrimSpace(identity)
		if identity == "" {
			continue
		}
		mergedSet[identity] = struct{}{}
	}
	if len(mergedSet) == 0 {
		return nil
	}

	merged := make([]string, 0, len(mergedSet))
	for identity := range mergedSet {
		merged = append(merged, identity)
	}
	sort.Strings(merged)
	return merged
}
