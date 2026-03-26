package controller

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/codex"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

type codexUsageCredential struct {
	KeyIndex int
	OAuthKey *codex.OAuthKey
}

func GetCodexChannelUsage(c *gin.Context) {
	channelId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiError(c, fmt.Errorf("invalid channel id: %w", err))
		return
	}

	ch, err := model.GetChannelById(channelId, true)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if ch == nil {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel not found"})
		return
	}
	if ch.Type != constant.ChannelTypeCodex {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "channel type is not Codex"})
		return
	}

	credentials := collectCodexUsageCredentials(ch)
	if len(credentials) == 0 {
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "no available codex credentials in channel"})
		return
	}

	client, err := service.NewProxyHttpClient(ch.GetSetting().Proxy)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	var (
		selectedCred *codexUsageCredential
		statusCode   int
		body         []byte
		lastErr      error
	)

	for i := range credentials {
		cred := &credentials[i]
		accessToken := strings.TrimSpace(cred.OAuthKey.AccessToken)
		accountID := strings.TrimSpace(cred.OAuthKey.AccountID)
		if accessToken == "" || accountID == "" {
			continue
		}

		reqCtx, reqCancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
		respStatus, respBody, fetchErr := service.FetchCodexWhamUsage(reqCtx, client, ch.GetBaseURL(), accessToken, accountID)
		reqCancel()

		if fetchErr != nil {
			lastErr = fetchErr
			continue
		}

		if (respStatus == http.StatusUnauthorized || respStatus == http.StatusForbidden) && strings.TrimSpace(cred.OAuthKey.RefreshToken) != "" {
			refreshCtx, refreshCancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
			res, refreshErr := service.RefreshCodexOAuthTokenWithProxy(refreshCtx, cred.OAuthKey.RefreshToken, ch.GetSetting().Proxy)
			refreshCancel()
			if refreshErr == nil {
				cred.OAuthKey.AccessToken = res.AccessToken
				cred.OAuthKey.RefreshToken = res.RefreshToken
				cred.OAuthKey.LastRefresh = time.Now().Format(time.RFC3339)
				cred.OAuthKey.Expired = res.ExpiresAt.Format(time.RFC3339)
				if strings.TrimSpace(cred.OAuthKey.Type) == "" {
					cred.OAuthKey.Type = "codex"
				}

				persistCodexUsageOAuthKey(ch, cred.KeyIndex, cred.OAuthKey)

				retryCtx, retryCancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
				respStatus, respBody, fetchErr = service.FetchCodexWhamUsage(retryCtx, client, ch.GetBaseURL(), cred.OAuthKey.AccessToken, accountID)
				retryCancel()
			}
		}

		if fetchErr != nil {
			lastErr = fetchErr
			continue
		}

		if selectedCred == nil {
			selectedCred = cred
			statusCode = respStatus
			body = respBody
		}

		if respStatus >= http.StatusOK && respStatus < http.StatusMultipleChoices {
			selectedCred = cred
			statusCode = respStatus
			body = respBody
			break
		}
		if !ch.ChannelInfo.IsMultiKey {
			break
		}
	}

	if selectedCred == nil || len(body) == 0 {
		if lastErr != nil {
			common.SysError("failed to fetch codex usage: " + lastErr.Error())
		} else {
			common.SysError("failed to fetch codex usage: no usable response")
		}
		c.JSON(http.StatusOK, gin.H{"success": false, "message": "failed to fetch codex usage"})
		return
	}

	var payload any
	if common.Unmarshal(body, &payload) != nil {
		payload = string(body)
	}

	ok := statusCode >= 200 && statusCode < 300
	resp := gin.H{
		"success":         ok,
		"message":         "",
		"upstream_status": statusCode,
		"data":            payload,
		"meta": gin.H{
			"is_multi_key":     ch.ChannelInfo.IsMultiKey,
			"key_index":        selectedCred.KeyIndex,
			"candidate_count":  len(credentials),
			"selected_email":   strings.TrimSpace(selectedCred.OAuthKey.Email),
			"selected_account": strings.TrimSpace(selectedCred.OAuthKey.AccountID),
		},
	}
	if !ok {
		resp["message"] = fmt.Sprintf("upstream status: %d", statusCode)
	}
	c.JSON(http.StatusOK, resp)
}

func collectCodexUsageCredentials(ch *model.Channel) []codexUsageCredential {
	result := make([]codexUsageCredential, 0)
	if ch == nil {
		return result
	}

	if !ch.ChannelInfo.IsMultiKey {
		oauthKey, err := codex.ParseOAuthKey(strings.TrimSpace(ch.Key))
		if err != nil {
			return result
		}
		if strings.TrimSpace(oauthKey.AccessToken) == "" || strings.TrimSpace(oauthKey.AccountID) == "" {
			return result
		}
		result = append(result, codexUsageCredential{
			KeyIndex: 0,
			OAuthKey: oauthKey,
		})
		return result
	}

	statusList := ch.ChannelInfo.MultiKeyStatusList
	keys := ch.GetKeys()
	for i, rawKey := range keys {
		if statusList != nil {
			if s, ok := statusList[i]; ok && s != common.ChannelStatusEnabled {
				continue
			}
		}
		oauthKey, err := codex.ParseOAuthKey(strings.TrimSpace(rawKey))
		if err != nil {
			continue
		}
		if strings.TrimSpace(oauthKey.AccessToken) == "" || strings.TrimSpace(oauthKey.AccountID) == "" {
			continue
		}
		result = append(result, codexUsageCredential{
			KeyIndex: i,
			OAuthKey: oauthKey,
		})
	}
	return result
}

func persistCodexUsageOAuthKey(ch *model.Channel, keyIndex int, oauthKey *codex.OAuthKey) {
	if ch == nil || oauthKey == nil {
		return
	}
	encoded, err := common.Marshal(oauthKey)
	if err != nil {
		return
	}
	newKey := strings.TrimSpace(string(encoded))
	if newKey == "" {
		return
	}

	if ch.ChannelInfo.IsMultiKey {
		keys := ch.GetKeys()
		if keyIndex < 0 || keyIndex >= len(keys) {
			return
		}
		keys[keyIndex] = newKey
		newKey = strings.Join(keys, "\n")
	}

	if err = model.DB.Model(&model.Channel{}).Where("id = ?", ch.Id).Update("key", newKey).Error; err != nil {
		return
	}
	ch.Key = newKey
	model.InitChannelCache()
	service.ResetProxyClientCache()
}
