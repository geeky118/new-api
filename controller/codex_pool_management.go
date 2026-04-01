package controller

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

func GetCodexPoolManagementAuthFiles(c *gin.Context) {
	channelID, err := resolveCodexPoolManagementChannelID(c)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	result, err := service.BuildCodexPoolSyncResultFromChannel(channelID, "")
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"mode":       "database_pool",
			"channel_id": channelID,
			"summary":    result,
			"files":      []string{},
		},
	})
}

func UploadCodexPoolManagementAuthFile(c *gin.Context) {
	channelID, err := resolveCodexPoolManagementChannelID(c)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	oauthKey, fileName, err := parseCodexPoolManagementUpload(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	result, err := service.ImportCodexChannelOAuthKey(channelID, oauthKey)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"message": "imported",
		"data": gin.H{
			"channel_id": channelID,
			"file_name":  fileName,
			"summary":    result,
		},
	})
}

func resolveCodexPoolManagementChannelID(c *gin.Context) (int, error) {
	channelID := common.GetEnvOrDefault("CODEX_POOL_CHANNEL_ID", 0)
	if rawID := strings.TrimSpace(c.Query("channel_id")); rawID != "" {
		parsed, err := strconv.Atoi(rawID)
		if err != nil || parsed <= 0 {
			return 0, fmt.Errorf("invalid channel_id")
		}
		channelID = parsed
	}
	if channelID <= 0 {
		return 0, fmt.Errorf("CODEX_POOL_CHANNEL_ID is not configured")
	}
	return channelID, nil
}

func parseCodexPoolManagementUpload(c *gin.Context) (*service.CodexOAuthKey, string, error) {
	contentType := strings.TrimSpace(strings.ToLower(c.GetHeader("Content-Type")))
	if strings.Contains(contentType, "multipart/form-data") {
		fileHeader, err := c.FormFile("file")
		if err != nil {
			return nil, "", fmt.Errorf("missing multipart file field: file")
		}

		file, err := fileHeader.Open()
		if err != nil {
			return nil, "", fmt.Errorf("open uploaded file failed: %w", err)
		}
		defer file.Close()

		var oauthKey service.CodexOAuthKey
		if err = common.DecodeJson(file, &oauthKey); err != nil {
			return nil, "", fmt.Errorf("invalid auth file json")
		}

		fileName := strings.TrimSpace(fileHeader.Filename)
		if fileName == "" {
			fileName = "auth-file.json"
		}
		return &oauthKey, fileName, nil
	}

	var oauthKey service.CodexOAuthKey
	if err := common.DecodeJson(c.Request.Body, &oauthKey); err != nil {
		return nil, "", fmt.Errorf("invalid auth file json")
	}

	fileName := strings.TrimSpace(c.Query("name"))
	if fileName == "" {
		email := strings.TrimSpace(strings.ToLower(oauthKey.Email))
		if email != "" {
			fileName = email + ".json"
		} else {
			fileName = "auth-file.json"
		}
	}

	return &oauthKey, fileName, nil
}
