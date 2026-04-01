package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/bytedance/gopkg/util/gopool"
)

type codexPoolAutoConfig struct {
	Enabled         bool
	ChannelID       int
	TokenDir        string
	DeleteSyncedFiles bool
	SyncInterval    time.Duration
	MinEnabledKeys  int
	RegisterEnabled bool
	RegisterBatch   int
	RegisterWorkers int
	RegisterTimeout time.Duration
	RegisterToolDir string
	RegisterPython  string
	RegisterNoOAuth bool
}

var (
	codexPoolAutoTaskOnce    sync.Once
	codexPoolAutoTaskRunning atomic.Bool
)

func StartCodexAccountPoolAutoUpdateTask() {
	codexPoolAutoTaskOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}

		gopool.Go(func() {
			logger.LogInfo(context.Background(), "codex pool auto-update task started")
			for {
				cfg := loadCodexPoolAutoConfigFromEnv()
				if !cfg.Enabled {
					time.Sleep(time.Minute)
					continue
				}
				if cfg.ChannelID <= 0 {
					logger.LogWarn(context.Background(), "codex pool auto-update disabled: CODEX_POOL_CHANNEL_ID is required")
					time.Sleep(time.Minute)
					continue
				}

				runCodexPoolAutoUpdateOnce(cfg)
				time.Sleep(cfg.SyncInterval)
			}
		})
	})
}

func loadCodexPoolAutoConfigFromEnv() codexPoolAutoConfig {
	poolSetting := operation_setting.GetCodexPoolSetting()

	intervalSec := common.GetEnvOrDefault("CODEX_POOL_SYNC_INTERVAL_SECONDS", 300)
	if poolSetting.AutoRegisterMinutes > 0 {
		intervalSec = poolSetting.AutoRegisterMinutes * 60
	}
	if intervalSec < 30 {
		intervalSec = 30
	}
	registerTimeoutSec := common.GetEnvOrDefault("CODEX_POOL_REGISTER_TIMEOUT_SECONDS", 1200)
	if registerTimeoutSec < 60 {
		registerTimeoutSec = 60
	}

	registerBatch := common.GetEnvOrDefault("CODEX_POOL_REGISTER_BATCH", 1)
	if registerBatch <= 0 {
		registerBatch = 1
	}
	registerWorkers := common.GetEnvOrDefault("CODEX_POOL_REGISTER_WORKERS", 1)
	if registerWorkers <= 0 {
		registerWorkers = 1
	}
	if poolSetting.RegisterWorkers > 0 {
		registerWorkers = poolSetting.RegisterWorkers
	}
	if poolSetting.RegisterCountPerInterval > 0 {
		registerBatch = poolSetting.RegisterCountPerInterval
	}

	minEnabledKeys := common.GetEnvOrDefault("CODEX_POOL_MIN_ENABLED_KEYS", 0)
	if poolSetting.MinEnabledKeys > 0 {
		minEnabledKeys = poolSetting.MinEnabledKeys
	}

	registerEnabled := common.GetEnvOrDefaultBool("CODEX_POOL_REGISTER_ENABLED", false)
	if poolSetting.AutoRegisterEnabled {
		registerEnabled = true
	}
	deleteSyncedFiles := common.GetEnvOrDefaultBool("CODEX_POOL_SYNC_DELETE_IMPORTED_FILES", true)
	if poolSetting.DeleteSyncedTokenFiles {
		deleteSyncedFiles = true
	}

	enabled := common.GetEnvOrDefaultBool("CODEX_POOL_AUTO_SYNC_ENABLED", false)
	if poolSetting.AutoRegisterEnabled {
		enabled = true
	}

	return codexPoolAutoConfig{
		Enabled:         enabled,
		ChannelID:       common.GetEnvOrDefault("CODEX_POOL_CHANNEL_ID", 0),
		TokenDir:        strings.TrimSpace(DefaultCodexPoolTokenDir()),
		DeleteSyncedFiles: deleteSyncedFiles,
		SyncInterval:    time.Duration(intervalSec) * time.Second,
		MinEnabledKeys:  minEnabledKeys,
		RegisterEnabled: registerEnabled,
		RegisterBatch:   registerBatch,
		RegisterWorkers: registerWorkers,
		RegisterTimeout: time.Duration(registerTimeoutSec) * time.Second,
		RegisterToolDir: strings.TrimSpace(DefaultCodexRegisterToolDir()),
		RegisterPython:  strings.TrimSpace(DefaultCodexRegisterPythonBin()),
		RegisterNoOAuth: common.GetEnvOrDefaultBool("CODEX_POOL_REGISTER_NO_OAUTH", false),
	}
}

func runCodexPoolAutoUpdateOnce(cfg codexPoolAutoConfig) {
	if !codexPoolAutoTaskRunning.CompareAndSwap(false, true) {
		return
	}
	defer codexPoolAutoTaskRunning.Store(false)

	ctx := context.Background()

	syncRes, err := SyncCodexChannelFromTokenDir(cfg.ChannelID, cfg.TokenDir)
	if err != nil {
		if cfg.DeleteSyncedFiles && (strings.Contains(err.Error(), "no valid codex oauth keys found") || strings.Contains(err.Error(), "no importable codex oauth keys found")) {
			syncRes, err = BuildCodexPoolSyncResultFromChannel(cfg.ChannelID, cfg.TokenDir)
		}
	}
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("codex pool auto-update: sync failed: %v", err))
		return
	}
	if common.DebugEnabled {
		logger.LogDebug(
			ctx,
			"codex pool auto-update: sync channel_id=%d updated=%t total_keys=%d enabled=%d disabled=%d files=%d/%d invalid=%d",
			syncRes.ChannelID,
			syncRes.Updated,
			syncRes.TotalKeys,
			syncRes.EnabledKeys,
			syncRes.DisabledKeys,
			syncRes.FilesLoaded,
			syncRes.FilesTotal,
			syncRes.FilesInvalid,
		)
	}
	if cfg.DeleteSyncedFiles {
		if deleted, deleteErr := DeleteAllCodexPoolTokenFiles(cfg.TokenDir); deleteErr != nil {
			logger.LogWarn(ctx, fmt.Sprintf("codex pool auto-update: delete synced token files failed: %v", deleteErr))
		} else if deleted > 0 {
			logger.LogInfo(ctx, fmt.Sprintf("codex pool auto-update: deleted %d synced token files after sync", deleted))
		}
	}

	if !cfg.RegisterEnabled || cfg.MinEnabledKeys <= 0 {
		return
	}

	if syncRes.EnabledKeys >= cfg.MinEnabledKeys {
		return
	}

	need := cfg.MinEnabledKeys - syncRes.EnabledKeys
	if need < cfg.RegisterBatch {
		need = cfg.RegisterBatch
	}
	roundCount := need
	if roundCount > cfg.RegisterBatch {
		roundCount = cfg.RegisterBatch
	}
	if roundCount <= 0 {
		roundCount = 1
	}
	if roundCount > 10 {
		roundCount = 10
	}

	roundWorkers := cfg.RegisterWorkers
	if roundWorkers <= 0 {
		roundWorkers = 1
	}
	if roundWorkers > roundCount {
		roundWorkers = roundCount
	}

	registerCtx, cancel := context.WithTimeout(ctx, cfg.RegisterTimeout)
	output, runErr := RunCodexRegisterTool(registerCtx, CodexRegisterRunOptions{
		ToolDir: cfg.RegisterToolDir,
		Python:  cfg.RegisterPython,
		Count:   roundCount,
		Workers: roundWorkers,
		NoOAuth: cfg.RegisterNoOAuth,
	})
	cancel()

	outputTail := tailString(output, 2000)
	if outputTail != "" {
		logger.LogInfo(ctx, fmt.Sprintf("codex pool auto-update: register output (tail):\n%s", outputTail))
	}
	if runErr != nil {
		logger.LogWarn(ctx, fmt.Sprintf("codex pool auto-update: register tool failed: %v", runErr))
		return
	}

	afterRes, err := SyncCodexChannelFromTokenDir(cfg.ChannelID, cfg.TokenDir)
	if err != nil {
		if cfg.DeleteSyncedFiles && (strings.Contains(err.Error(), "no valid codex oauth keys found") || strings.Contains(err.Error(), "no importable codex oauth keys found")) {
			afterRes, err = BuildCodexPoolSyncResultFromChannel(cfg.ChannelID, cfg.TokenDir)
		}
	}
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("codex pool auto-update: sync after register failed: %v", err))
		return
	}

	if afterRes.EnabledKeys >= cfg.MinEnabledKeys {
		if cfg.DeleteSyncedFiles {
			if deleted, deleteErr := DeleteAllCodexPoolTokenFiles(cfg.TokenDir); deleteErr != nil {
				logger.LogWarn(ctx, fmt.Sprintf("codex pool auto-update: delete synced token files after register failed: %v", deleteErr))
			} else if deleted > 0 {
				logger.LogInfo(ctx, fmt.Sprintf("codex pool auto-update: deleted %d synced token files after register", deleted))
			}
		}
		logger.LogInfo(
			ctx,
			fmt.Sprintf(
				"codex pool auto-update: replenished channel_id=%d enabled=%d/%d total_keys=%d updated=%t",
				afterRes.ChannelID,
				afterRes.EnabledKeys,
				cfg.MinEnabledKeys,
				afterRes.TotalKeys,
				afterRes.Updated,
			),
		)
		return
	}

	if cfg.DeleteSyncedFiles {
		if deleted, deleteErr := DeleteAllCodexPoolTokenFiles(cfg.TokenDir); deleteErr != nil {
			logger.LogWarn(ctx, fmt.Sprintf("codex pool auto-update: delete synced token files after partial register failed: %v", deleteErr))
		} else if deleted > 0 {
			logger.LogInfo(ctx, fmt.Sprintf("codex pool auto-update: deleted %d synced token files after partial register", deleted))
		}
	}

	logger.LogWarn(
		ctx,
		fmt.Sprintf(
			"codex pool auto-update: target not reached in this cycle, enabled=%d target=%d",
			afterRes.EnabledKeys,
			cfg.MinEnabledKeys,
		),
	)
}

func tailString(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	s := strings.TrimSpace(text)
	if s == "" {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= maxRunes {
		return s
	}
	return string(runes[len(runes)-maxRunes:])
}
