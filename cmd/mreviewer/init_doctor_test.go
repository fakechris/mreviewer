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
	if !strings.Contains(content, "models:") {
		t.Fatalf("config missing models section: %s", content)
	}
	if !strings.Contains(content, "model_chains:") {
		t.Fatalf("config missing model_chains section: %s", content)
	}
	if !strings.Contains(content, "review:\n  model_chain: review_primary") {
		t.Fatalf("config missing review model chain: %s", content)
	}
	if !strings.Contains(content, "openai_default:") {
		t.Fatalf("config missing openai model id: %s", content)
	}
	if !strings.Contains(content, "provider: openai") {
		t.Fatalf("config missing provider stanza: %s", content)
	}
	if !strings.Contains(content, "output_mode: tool_call") {
		t.Fatalf("config missing tool_call default: %s", content)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, ".mreviewer/state")); err != nil {
		t.Fatalf("expected state dir: %v", err)
	}
}

func TestRunInitCommandDryRunPrintsConfigWithoutWriting(t *testing.T) {
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
	exitCode := runInitCommand([]string{"--config", configPath, "--provider", "openai", "--dry-run"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("config file exists after dry-run, err=%v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "app_env: development") {
		t.Fatalf("dry-run output missing config body: %q", output)
	}
	if !strings.Contains(output, "output_mode: tool_call") {
		t.Fatalf("dry-run output missing tool_call default: %q", output)
	}
	if !strings.Contains(output, "# dry-run: config was not written") {
		t.Fatalf("dry-run output missing dry-run marker: %q", output)
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
models:
  openai_default:
    provider: openai
    api_key: "test-key"
    base_url: "https://api.openai.com/v1"
    model: "gpt-5.4"
    output_mode: "tool_call"
    max_completion_tokens: 12000
model_chains:
  review_primary:
    primary: openai_default
review:
  model_chain: review_primary
  packs:
    - security
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
	if !strings.Contains(content, "model_chains:") {
		t.Fatalf("config missing model_chains section: %s", content)
	}
	if !strings.Contains(content, "review:\n  model_chain: review_primary") {
		t.Fatalf("config missing review section: %s", content)
	}
	if !strings.Contains(content, "max_tokens: 4096") {
		t.Fatalf("config missing minimax max_tokens stanza: %s", content)
	}
	if strings.Contains(content, "reasoning_effort:") {
		t.Fatalf("config should omit empty reasoning stanza: %s", content)
	}
}

func TestRenderPersonalConfigZhipuAI(t *testing.T) {
	content, err := renderPersonalConfig("zhipuai")
	if err != nil {
		t.Fatalf("renderPersonalConfig: %v", err)
	}
	if !strings.Contains(content, "zhipuai_default:") {
		t.Fatalf("config missing zhipuai model id: %s", content)
	}
	if !strings.Contains(content, "provider: zhipuai") {
		t.Fatalf("config missing zhipuai provider: %s", content)
	}
	if !strings.Contains(content, "base_url: https://open.bigmodel.cn/api/coding/paas/v4") {
		t.Fatalf("config missing zhipuai base url: %s", content)
	}
	if !strings.Contains(content, "api_key: ${ZHIPUAI_API_KEY}") {
		t.Fatalf("config missing zhipuai api key env: %s", content)
	}
	if !strings.Contains(content, "model: glm-5") {
		t.Fatalf("config missing glm-5 model: %s", content)
	}
	if !strings.Contains(content, "output_mode: tool_call") {
		t.Fatalf("config missing tool_call mode: %s", content)
	}
}

func TestRunInitThenDoctorUsesProviderDefaultBaseURLWhenEnvUnset(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() { _ = os.Chdir(wd) }()

	t.Setenv("OPENAI_API_KEY", "test-openai-key")
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("GITHUB_TOKEN", "test-github-token")
	t.Setenv("GITHUB_BASE_URL", "")
	t.Setenv("GITLAB_TOKEN", "")
	t.Setenv("GITLAB_BASE_URL", "")

	configPath := filepath.Join(tmpDir, "config.yaml")
	var initStdout bytes.Buffer
	var initStderr bytes.Buffer
	if exitCode := runInitCommand([]string{"--config", configPath, "--provider", "openai"}, &initStdout, &initStderr); exitCode != 0 {
		t.Fatalf("init exitCode = %d, want 0 (stderr=%s)", exitCode, initStderr.String())
	}

	var doctorStdout bytes.Buffer
	var doctorStderr bytes.Buffer
	exitCode := runDoctorCommand([]string{"--config", configPath, "--json"}, &doctorStdout, &doctorStderr)
	if exitCode != 0 {
		t.Fatalf("doctor exitCode = %d, want 0 (stdout=%s stderr=%s)", exitCode, doctorStdout.String(), doctorStderr.String())
	}

	var report doctorReport
	if err := json.Unmarshal(doctorStdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if !report.OK {
		t.Fatalf("report.OK = false, want true: %+v", report.Checks)
	}
}

func TestRunDoctorCommandWarnsForIncompleteGitLabWhenGitHubIsConfigured(t *testing.T) {
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
gitlab_token: "gitlab-token"
models:
  openai_default:
    provider: openai
    api_key: "test-key"
    base_url: "https://api.openai.com/v1"
    model: "gpt-5.4"
    output_mode: "tool_call"
    max_completion_tokens: 12000
model_chains:
  review_primary:
    primary: openai_default
review:
  model_chain: review_primary
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("GITHUB_TOKEN", "test-github-token")
	t.Setenv("GITHUB_BASE_URL", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runDoctorCommand([]string{"--config", configPath, "--json"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stdout=%s stderr=%s)", exitCode, stdout.String(), stderr.String())
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if !report.OK {
		t.Fatalf("report.OK = false, want true: %+v", report.Checks)
	}
	for _, check := range report.Checks {
		if check.Name == "gitlab" && check.Status == "fail" {
			t.Fatalf("gitlab check = fail, want warn: %+v", check)
		}
	}
}

func TestRunDoctorCommandWarnsForOpenAICompatibleJSONSchemaRoutes(t *testing.T) {
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
models:
  zhipu_probe:
    provider: zhipuai
    api_key: "test-key"
    base_url: "https://open.bigmodel.cn/api/coding/paas/v4"
    model: "glm-5"
    output_mode: "json_schema"
model_chains:
  review_primary:
    primary: zhipu_probe
review:
  model_chain: review_primary
`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("GITHUB_TOKEN", "test-github-token")
	t.Setenv("GITHUB_BASE_URL", "")

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runDoctorCommand([]string{"--config", configPath, "--json"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stdout=%s stderr=%s)", exitCode, stdout.String(), stderr.String())
	}
	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	foundWarn := false
	for _, check := range report.Checks {
		if check.Name == "structured_output_strategy" && check.Status == "warn" {
			foundWarn = true
		}
	}
	if !foundWarn {
		t.Fatalf("expected structured_output_strategy warning: %+v", report.Checks)
	}
}
