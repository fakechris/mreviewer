package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLIStructuredOutputProbeSubcommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCLI([]string{"structured-output-probe", "--help"}, runtimeDeps{
		stdout: &stdout,
		stderr: &stderr,
	})
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}
	helpText := stdout.String() + stderr.String()
	if !strings.Contains(helpText, "--route") {
		t.Fatalf("help missing --route: %q", helpText)
	}
	if !strings.Contains(helpText, "--mode") {
		t.Fatalf("help missing --mode: %q", helpText)
	}
}

func TestRunStructuredOutputProbeCommandOpenAITool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"finish_reason":"tool_calls","message":{"content":"","reasoning_content":"reasoning","tool_calls":[{"type":"function","function":{"name":"StructuredOutput","arguments":"{\"verdict\":\"pass\",\"score\":0.93}"}}]}}],"model":"glm-5.1"}`)
	}))
	defer server.Close()

	configPath := writeProbeConfig(t, fmt.Sprintf(`
models:
  zhipu_probe:
    provider: zhipuai
    base_url: %s
    api_key: test-key
    model: glm-5
`, server.URL))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runStructuredOutputProbeCommand([]string{"--config", configPath, "--route", "zhipu_probe", "--mode", "tool"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}

	var payload structuredOutputProbeOutput
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\nstdout=%s", err, stdout.String())
	}
	if payload.HTTPOKCount != 1 || payload.ParsedOKCount != 1 || payload.SchemaOKCount != 1 {
		t.Fatalf("unexpected counts: %+v", payload)
	}
	if got := payload.Results[0].FinishReason; got != "tool_calls" {
		t.Fatalf("finish_reason = %q, want tool_calls", got)
	}
}

func TestRunStructuredOutputProbeCommandOpenAINativeFencedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, "{\"choices\":[{\"finish_reason\":\"stop\",\"message\":{\"content\":\"```json\\n{\\\"verdict\\\":\\\"pass\\\",\\\"score\\\":0.93}\\n```\",\"reasoning_content\":\"reasoning\"}}],\"model\":\"glm-5.1\"}")
	}))
	defer server.Close()

	configPath := writeProbeConfig(t, fmt.Sprintf(`
models:
  zhipu_probe:
    provider: openai
    base_url: %s
    api_key: test-key
    model: glm-5
`, server.URL))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runStructuredOutputProbeCommand([]string{"--config", configPath, "--route", "zhipu_probe", "--mode", "native"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}

	var payload structuredOutputProbeOutput
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\nstdout=%s", err, stdout.String())
	}
	if payload.ParsedOKCount != 1 || payload.SchemaOKCount != 1 {
		t.Fatalf("unexpected counts: %+v", payload)
	}
	if got := payload.Results[0].StructuredOutput["verdict"]; got != "pass" {
		t.Fatalf("verdict = %#v, want pass", got)
	}
}

func TestRunStructuredOutputProbeCommandOpenAINativeParsesFullBodyNotPreview(t *testing.T) {
	longJSON := "```json\n{\n" +
		strings.Repeat(" ", 260) +
		"\"verdict\": \"pass\",\n" +
		"\"score\": 0.93\n}\n```"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"choices":[{"finish_reason":"stop","message":{"content":%q,"reasoning_content":"reasoning"}}],"model":"glm-5.1"}`, longJSON)
	}))
	defer server.Close()

	configPath := writeProbeConfig(t, fmt.Sprintf(`
models:
  zhipu_probe:
    provider: openai
    base_url: %s
    api_key: test-key
    model: glm-5
`, server.URL))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runStructuredOutputProbeCommand([]string{"--config", configPath, "--route", "zhipu_probe", "--mode", "native"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}

	var payload structuredOutputProbeOutput
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\nstdout=%s", err, stdout.String())
	}
	if payload.SchemaOKCount != 1 {
		t.Fatalf("schema_ok_count = %d, want 1", payload.SchemaOKCount)
	}
}

func TestRunStructuredOutputProbeCommandAnthropicTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"model":"MiniMax-M2.7","stop_reason":"tool_use","content":[{"type":"thinking","thinking":"reasoning"},{"type":"tool_use","name":"StructuredOutput","input":{"verdict":"pass","score":0.93}}]}`)
	}))
	defer server.Close()

	configPath := writeProbeConfig(t, fmt.Sprintf(`
models:
  minimax_probe:
    provider: minimax
    base_url: %s
    api_key: test-key
    model: MiniMax-M2.7
`, server.URL))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runStructuredOutputProbeCommand([]string{"--config", configPath, "--route", "minimax_probe", "--mode", "tool"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}

	var payload structuredOutputProbeOutput
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal output: %v\nstdout=%s", err, stdout.String())
	}
	if got := payload.Results[0].FinishReason; got != "tool_use" {
		t.Fatalf("finish_reason = %q, want tool_use", got)
	}
	if payload.SchemaOKCount != 1 {
		t.Fatalf("schema_ok_count = %d, want 1", payload.SchemaOKCount)
	}
}

func TestRunStructuredOutputProbeCommandRejectsAnthropicNativeMode(t *testing.T) {
	configPath := writeProbeConfig(t, `
models:
  minimax_probe:
    provider: minimax
    base_url: https://api.minimaxi.com/anthropic
    api_key: test-key
    model: MiniMax-M2.7
`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runStructuredOutputProbeCommand([]string{"--config", configPath, "--route", "minimax_probe", "--mode", "native"}, &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2 (stderr=%s)", exitCode, stderr.String())
	}
}

func writeProbeConfig(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(body)+"\n"), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}
