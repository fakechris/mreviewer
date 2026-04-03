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
	"sync/atomic"
	"testing"
)

func TestRunCLISchemaBenchmarkSubcommand(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runCLI([]string{"schema-benchmark", "--help"}, runtimeDeps{
		stdout: &stdout,
		stderr: &stderr,
	})

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}
	helpOutput := stdout.String() + stderr.String()
	if !strings.Contains(helpOutput, "--routes") {
		t.Fatalf("help output missing --routes: %q", helpOutput)
	}
	if !strings.Contains(helpOutput, "--input") {
		t.Fatalf("help output missing --input: %q", helpOutput)
	}
}

func TestRunSchemaBenchmarkCommandOutputsSchemaAccuracySummary(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := atomic.AddInt32(&calls, 1)
		w.Header().Set("Content-Type", "application/json")
		switch call {
		case 1:
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"submit_review","arguments":"{\"schema_version\":\"1.0\",\"review_run_id\":\"rr-1\",\"summary\":\"ok\",\"findings\":[{\"severity\":\"high\",\"confidence\":0.91,\"title\":\"Issue\",\"body_markdown\":\"body\",\"path\":\"main.go\",\"anchor_kind\":\"new_line\",\"new_line\":5}]}"}}]}}],"usage":{"completion_tokens":42}}`)
		default:
			_, _ = fmt.Fprint(w, `{"choices":[{"message":{"tool_calls":[{"type":"function","function":{"name":"submit_review","arguments":"{\"schema_version\":\"1.0\",\"review_run_id\":\"rr-1\",\"summary\":\"ok\",\"findings\":[{\"category\":\"bug\",\"severity\":\"high\",\"confidence\":0.91,\"title\":\"Issue\",\"body_markdown\":\"body\",\"path\":\"main.go\",\"anchor_kind\":\"new_line\",\"new_line\":5}]}"}}]}}],"usage":{"completion_tokens":21}}`)
		}
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config.yaml")
	inputPath := filepath.Join(tmpDir, "requests.jsonl")
	if err := os.WriteFile(configPath, []byte(fmt.Sprintf(`
models:
  kimi_turbo:
    provider: openai
    base_url: %s
    api_key: test-token
    model: kimi-turbo
    output_mode: tool_call
`, server.URL)), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := os.WriteFile(inputPath, []byte("{\"schema_version\":\"1.0\",\"review_run_id\":\"rr-1\"}\n"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runSchemaBenchmarkCommand([]string{"--config", configPath, "--routes", "kimi_turbo", "--input", inputPath}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0 (stderr=%s)", exitCode, stderr.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("json output: %v\nstdout=%s", err, stdout.String())
	}
	routes, ok := payload["routes"].([]any)
	if !ok || len(routes) != 1 {
		t.Fatalf("routes = %#v, want one route report", payload["routes"])
	}
	report := routes[0].(map[string]any)
	if report["initial_schema_accuracy"] != 0.0 {
		t.Fatalf("initial_schema_accuracy = %#v, want 0", report["initial_schema_accuracy"])
	}
	if report["repair_rate"] != 1.0 {
		t.Fatalf("repair_rate = %#v, want 1", report["repair_rate"])
	}
	if report["final_success_rate"] != 1.0 {
		t.Fatalf("final_success_rate = %#v, want 1", report["final_success_rate"])
	}
	if _, ok := report["failure_reasons"]; !ok {
		t.Fatalf("failure_reasons missing from report: %#v", report)
	}
}
