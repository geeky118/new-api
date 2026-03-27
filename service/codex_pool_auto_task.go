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

	"github.com/bytedance/gopkg/util/gopool"
)

type codexPoolAutoConfig struct {
	Enabled              bool
	ChannelID            int
	TokenDir             string
	SyncInterval         time.Duration
	MinEnabledKeys       int
	RegisterEnabled      bool
	RegisterBatch        int
	RegisterMaxPerRound  int
	RegisterRoundInterval time.Duration
	RegisterWorkers      int
	RegisterTimeout      time.Duration
	RegisterToolDir      string
	RegisterPython       string
	RegisterNoOAuth      bool
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

		cfg := loadCodexPoolAutoConfigFromEnv()
		if !cfg.Enabled {
			return
		}
		if cfg.ChannelID <= 0 {
			logger.LogWarn(context.Background(), "codex pool auto-update disabled: CODEX_POOL_CHANNEL_ID is required")
			return
		}

		gopool.Go(func() {
			logger.LogInfo(context.Background(),
				fmt.Sprintf(
					"codex pool auto-update task started: channel_id=%d token_dir=%s interval=%s min_enabled=%d register_enabled=%t",
					cfg.ChannelID, cfg.TokenDir, cfg.SyncInterval, cfg.MinEnabledKeys, cfg.RegisterEnabled,
				),
			)

			runCodexPoolAutoUpdateOnce(cfg)

			ticker := time.NewTicker(cfg.SyncInterval)
			defer ticker.Stop()
			for range ticker.C {
				runCodexPoolAutoUpdateOnce(cfg)
			}
		})
	})
}

func loadCodexPoolAutoConfigFromEnv() codexPoolAutoConfig {
	intervalSec := common.GetEnvOrDefault("CODEX_POOL_SYNC_INTERVAL_SECONDS", 300)
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
	registerMaxPerRound := common.GetEnvOrDefault("CODEX_POOL_REGISTER_MAX_PER_ROUND", 10)
	if registerMaxPerRound <= 0 {
		registerMaxPerRound = 10
	}
	if registerMaxPerRound > 10 {
		registerMaxPerRound = 10
	}
	roundIntervalSec := common.GetEnvOrDefault("CODEX_POOL_REGISTER_ROUND_INTERVAL_SECONDS", 90)
	if roundIntervalSec < 10 {
		roundIntervalSec = 10
	}

	return codexPoolAutoConfig{
		Enabled:               common.GetEnvOrDefaultBool("CODEX_POOL_AUTO_SYNC_ENABLED", false),
		ChannelID:             common.GetEnvOrDefault("CODEX_POOL_CHANNEL_ID", 0),
		TokenDir:              strings.TrimSpace(DefaultCodexPoolTokenDir()),
		SyncInterval:          time.Duration(intervalSec) * time.Second,
		MinEnabledKeys:        common.GetEnvOrDefault("CODEX_POOL_MIN_ENABLED_KEYS", 0),
		RegisterEnabled:       common.GetEnvOrDefaultBool("CODEX_POOL_REGISTER_ENABLED", false),
		RegisterBatch:         registerBatch,
		RegisterMaxPerRound:   registerMaxPerRound,
		RegisterRoundInterval: time.Duration(roundIntervalSec) * time.Second,
		RegisterWorkers:       registerWorkers,
		RegisterTimeout:       time.Duration(registerTimeoutSec) * time.Second,
		RegisterToolDir:       strings.TrimSpace(DefaultCodexRegisterToolDir()),
		RegisterPython:        strings.TrimSpace(DefaultCodexRegisterPythonBin()),
		RegisterNoOAuth:       common.GetEnvOrDefaultBool("CODEX_POOL_REGISTER_NO_OAUTH", false),
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
	perRound := cfg.RegisterBatch
	if perRound <= 0 {
		perRound = 1
	}
	if perRound > cfg.RegisterMaxPerRound {
		perRound = cfg.RegisterMaxPerRound
	}
	if perRound > 10 {
		perRound = 10
	}
	if perRound <= 0 {
		perRound = 1
	}

	deadline := time.Now().Add(cfg.RegisterTimeout)
	remaining := need
	enabledNow := syncRes.EnabledKeys

	for remaining > 0 {
		left := time.Until(deadline)
		if left <= 0 {
			logger.LogWarn(ctx, "codex pool auto-update: register timeout reached before target size")
			break
		}

		roundCount := remaining
		if roundCount > perRound {
			roundCount = perRound
		}
		roundWorkers := cfg.RegisterWorkers
		if roundWorkers <= 0 {
			roundWorkers = 1
		}
		if roundWorkers > roundCount {
			roundWorkers = roundCount
		}

		registerCtx, cancel := context.WithTimeout(ctx, left)
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
			logger.LogWarn(ctx, fmt.Sprintf("codex pool auto-update: sync after register failed: %v", err))
			return
		}

		if afterRes.EnabledKeys >= cfg.MinEnabledKeys {
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

		if afterRes.EnabledKeys <= enabledNow {
			logger.LogWarn(
				ctx,
				fmt.Sprintf(
					"codex pool auto-update: register made no progress (enabled=%d target=%d), stop this cycle",
					afterRes.EnabledKeys,
					cfg.MinEnabledKeys,
				),
			)
			return
		}

		enabledNow = afterRes.EnabledKeys
		remaining = cfg.MinEnabledKeys - enabledNow

		if remaining > 0 && cfg.RegisterRoundInterval > 0 {
			time.Sleep(cfg.RegisterRoundInterval)
		}
	}

	if enabledNow < cfg.MinEnabledKeys {
		logger.LogWarn(
			ctx,
			fmt.Sprintf(
				"codex pool auto-update: target not reached in this cycle, enabled=%d target=%d",
				enabledNow,
				cfg.MinEnabledKeys,
			),
		)
	}
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
