package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type CodexPoolSetting struct {
	AutoHealthCheckEnabled       bool `json:"auto_health_check_enabled"`
	AutoHealthCheckMinutes       int  `json:"auto_health_check_minutes"`
	DeleteInvalidTokenFiles      bool `json:"delete_invalid_token_files"`
	AutoRegisterEnabled          bool `json:"auto_register_enabled"`
	MinEnabledKeys               int  `json:"min_enabled_keys"`
	AutoRegisterMinutes          int  `json:"auto_register_minutes"`
	RegisterCountPerInterval     int  `json:"register_count_per_interval"`
	RegisterWorkers              int  `json:"register_workers"`
}

var codexPoolSetting = CodexPoolSetting{
	AutoHealthCheckEnabled:   false,
	AutoHealthCheckMinutes:   10,
	DeleteInvalidTokenFiles:  false,
	AutoRegisterEnabled:      false,
	MinEnabledKeys:           70,
	AutoRegisterMinutes:      5,
	RegisterCountPerInterval: 1,
	RegisterWorkers:          1,
}

func init() {
	config.GlobalConfig.Register("codex_pool_setting", &codexPoolSetting)
}

func GetCodexPoolSetting() *CodexPoolSetting {
	return &codexPoolSetting
}
