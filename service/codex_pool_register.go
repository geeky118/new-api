package service

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

type CodexRegisterRunOptions struct {
	ToolDir  string
	Python   string
	Count    int
	Workers  int
	NoOAuth  bool
	SyncDir  string
	ChannelID int
}

func DefaultCodexRegisterToolDir() string {
	return common.GetEnvOrDefaultString("CODEX_POOL_REGISTER_TOOL_DIR", "chatgpt_register_v2_by_AI")
}

func DefaultCodexRegisterPythonBin() string {
	return common.GetEnvOrDefaultString("CODEX_POOL_PYTHON_BIN", "python")
}

func RunCodexRegisterTool(ctx context.Context, opts CodexRegisterRunOptions) (string, error) {
	toolDir := strings.TrimSpace(opts.ToolDir)
	if toolDir == "" {
		toolDir = DefaultCodexRegisterToolDir()
	}
	absToolDir, err := filepath.Abs(toolDir)
	if err == nil {
		toolDir = absToolDir
	}

	pythonBin := strings.TrimSpace(opts.Python)
	if pythonBin == "" {
		pythonBin = DefaultCodexRegisterPythonBin()
	}

	count := opts.Count
	if count <= 0 {
		count = 1
	}
	workers := opts.Workers
	if workers <= 0 {
		workers = 1
	}

	args := []string{
		"chatgpt_register_v2.py",
		"-n", strconv.Itoa(count),
		"-w", strconv.Itoa(workers),
	}
	if opts.NoOAuth {
		args = append(args, "--no-oauth")
	}

	cmd := exec.CommandContext(ctx, pythonBin, args...)
	cmd.Dir = toolDir

	var combined bytes.Buffer
	cmd.Stdout = &combined
	cmd.Stderr = &combined

	runErr := cmd.Run()
	output := combined.String()
	if runErr != nil {
		return output, fmt.Errorf("run register tool failed: %w", runErr)
	}
	return output, nil
}

