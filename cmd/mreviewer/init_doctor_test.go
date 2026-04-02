package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunInitCommandWritesConfig(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	configPath := filepath.Join(tmpDir, "config.yaml")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runInitCommand([]string{"--config", configPath, "--provider", "openai"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, "database_dsn: \"file:.mreviewer/state/mreviewer.db?_pragma=busy_timeout(5000)\"") {
		t.Fatalf("config missing sqlite dsn: %s", content)
	}
	if !strings.Contains(content, "provider: openai") {
		t.Fatalf("config missing provider stanza: %s", content)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, ".mreviewer/state")); err != nil {
		t.Fatalf("expected state dir: %v", err)
	}
}

func TestRunDoctorCommandJSONReportsMissingPlatformTokens(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	configPath := filepath.Join(tmpDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(`
app_env: development
database_dsn: "file:.mreviewer/state/mreviewer.db?_pragma=busy_timeout(5000)"
llm_provider: openai
llm_api_key: "test-key"
llm_base_url: "https://api.openai.com/v1"
llm_model: "gpt-5.4"
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runDoctorCommand([]string{"--config", configPath, "--json"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1 (stderr=%s)", exitCode, stderr.String())
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if report.OK {
		t.Fatalf("report.OK = true, want false")
	}
	foundPlatformsFailure := false
	for _, check := range report.Checks {
		if check.Name == "platforms" && check.Status == "fail" {
			foundPlatformsFailure = true
		}
	}
	if !foundPlatformsFailure {
		t.Fatalf("expected platforms failure in report: %+v", report.Checks)
	}
}

func TestRenderPersonalConfigOmitsBlankOptionalLines(t *testing.T) {
	content, err := renderPersonalConfig("minimax")
	if err != nil {
		t.Fatalf("renderPersonalConfig: %v", err)
	}
	if strings.Contains(content, "\n      \n") {
		t.Fatalf("config contains indented blank optional line: %q", content)
	}
	if !strings.Contains(content, "max_tokens: 4096") {
		t.Fatalf("config missing minimax max_tokens stanza: %s", content)
	}
	if strings.Contains(content, "reasoning_effort:") {
		t.Fatalf("config should omit empty reasoning stanza: %s", content)
	}
}
