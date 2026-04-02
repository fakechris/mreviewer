package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mreviewer/mreviewer/internal/config"
	"github.com/mreviewer/mreviewer/internal/db"
	"github.com/mreviewer/mreviewer/internal/manualtrigger"
)

type fakeManualTriggerService struct {
	triggerResult manualtrigger.TriggerResult
	triggerErr    error
	triggerInput  manualtrigger.TriggerInput
	waitResult    db.ReviewRun
	waitErr       error
	waitRunID     int64
}

func (f *fakeManualTriggerService) Trigger(_ context.Context, input manualtrigger.TriggerInput) (manualtrigger.TriggerResult, error) {
	f.triggerInput = input
	return f.triggerResult, f.triggerErr
}

func (f *fakeManualTriggerService) WaitForTerminalRun(_ context.Context, runID int64) (db.ReviewRun, error) {
	f.waitRunID = runID
	return f.waitResult, f.waitErr
}

func TestRunWithDepsJSONOutputWithoutWait(t *testing.T) {
	svc := &fakeManualTriggerService{
		triggerResult: manualtrigger.TriggerResult{
			RunID:          101,
			ProjectID:      123,
			MRIID:          45,
			HeadSHA:        "head-sha-123",
			IdempotencyKey: "idem-123",
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--project-id", "123", "--mr-iid", "45", "--json"}, runtimeDeps{
		loadConfig: func(string) (*config.Config, error) { return &config.Config{}, nil },
		openDB:     func(string) (*sql.DB, error) { return nil, nil },
		newService: func(*config.Config, *sql.DB, time.Duration) manualTriggerService { return svc },
		stdout:     &stdout,
		stderr:     &stderr,
	})

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var payload jsonResponse
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal stdout: %v", err)
	}

	if !payload.OK {
		t.Fatalf("payload.OK = false, want true")
	}
	if payload.Waited {
		t.Fatalf("payload.Waited = true, want false")
	}
	if payload.Created == nil || payload.Created.RunID != 101 {
		t.Fatalf("payload.Created = %#v, want run_id 101", payload.Created)
	}
	if payload.Terminal != nil {
		t.Fatalf("payload.Terminal = %#v, want nil", payload.Terminal)
	}
}

func TestRunWithDepsPassesLLMRouteToService(t *testing.T) {
	svc := &fakeManualTriggerService{
		triggerResult: manualtrigger.TriggerResult{
			RunID:          104,
			ProjectID:      123,
			MRIID:          48,
			HeadSHA:        "head-sha-route",
			IdempotencyKey: "idem-route",
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--project-id", "123", "--mr-iid", "48", "--llm-route", "openai-gpt-5-4"}, runtimeDeps{
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{
				Models: map[string]config.ModelConfig{
					"openai-gpt-5-4": {Provider: "openai", BaseURL: "https://api.openai.com/v1", APIKey: "secret", Model: "gpt-5.4"},
				},
			}, nil
		},
		openDB:     func(string) (*sql.DB, error) { return nil, nil },
		newService: func(*config.Config, *sql.DB, time.Duration) manualTriggerService { return svc },
		stdout:     &stdout,
		stderr:     &stderr,
	})

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if svc.triggerInput.ProviderRoute != "openai-gpt-5-4" {
		t.Fatalf("triggerInput.ProviderRoute = %q, want openai-gpt-5-4", svc.triggerInput.ProviderRoute)
	}
}

func TestRunWithDepsRejectsConflictingProviderRouteAliases(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{
		"--project-id", "123",
		"--mr-iid", "48",
		"--llm-route", "openai-gpt-5-4",
		"--provider-route", "claude-opus-4-6",
	}, runtimeDeps{
		loadConfig: func(string) (*config.Config, error) { return &config.Config{}, nil },
		openDB:     func(string) (*sql.DB, error) { return nil, nil },
		newService: func(*config.Config, *sql.DB, time.Duration) manualTriggerService { return &fakeManualTriggerService{} },
		stdout:     &stdout,
		stderr:     &stderr,
	})

	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "--llm-route and --provider-route must match") {
		t.Fatalf("stderr = %q, want conflicting alias validation", stderr.String())
	}
}

func TestRunWithDepsRejectsEmptyProviderRouteOverride(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{
		"--project-id", "123",
		"--mr-iid", "48",
		"--llm-route=",
	}, runtimeDeps{
		loadConfig: func(string) (*config.Config, error) { return &config.Config{}, nil },
		openDB:     func(string) (*sql.DB, error) { return nil, nil },
		newService: func(*config.Config, *sql.DB, time.Duration) manualTriggerService { return &fakeManualTriggerService{} },
		stdout:     &stdout,
		stderr:     &stderr,
	})

	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "--llm-route must not be empty") {
		t.Fatalf("stderr = %q, want empty override validation", stderr.String())
	}
}

func TestRunWithDepsAllowsConfiguredModelOverride(t *testing.T) {
	svc := &fakeManualTriggerService{
		triggerResult: manualtrigger.TriggerResult{
			RunID:          105,
			ProjectID:      123,
			MRIID:          49,
			HeadSHA:        "head-sha-default",
			IdempotencyKey: "idem-default",
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--project-id", "123", "--mr-iid", "49", "--llm-route", "default"}, runtimeDeps{
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{
				Models: map[string]config.ModelConfig{
					"default": {Provider: "openai", Model: "gpt-5.4"},
				},
			}, nil
		},
		openDB:     func(string) (*sql.DB, error) { return nil, nil },
		newService: func(*config.Config, *sql.DB, time.Duration) manualTriggerService { return svc },
		stdout:     &stdout,
		stderr:     &stderr,
	})

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if svc.triggerInput.ProviderRoute != "default" {
		t.Fatalf("triggerInput.ProviderRoute = %q, want default", svc.triggerInput.ProviderRoute)
	}
}

func TestValidateProviderRouteOverrideReturnsSortedRoutes(t *testing.T) {
	err := validateProviderRouteOverride(&config.Config{
		Models: map[string]config.ModelConfig{
			"zulu":  {},
			"alpha": {},
		},
		ModelChains: map[string]config.ModelChainConfig{
			"review_primary": {},
		},
	}, "missing")
	if err == nil {
		t.Fatal("expected unknown provider route error")
	}
	if !strings.Contains(err.Error(), "available: alpha, review_primary, zulu") {
		t.Fatalf("error = %q, want sorted available routes", err.Error())
	}
}

func TestValidateProviderRouteOverrideAcceptsModelChainReference(t *testing.T) {
	err := validateProviderRouteOverride(&config.Config{
		Models: map[string]config.ModelConfig{
			"default": {},
		},
		ModelChains: map[string]config.ModelChainConfig{
			"review_primary": {Primary: "default"},
		},
	}, "review_primary")
	if err != nil {
		t.Fatalf("validateProviderRouteOverride() error = %v, want nil", err)
	}
}

func TestRunWithDepsJSONOutputWithWait(t *testing.T) {
	svc := &fakeManualTriggerService{
		triggerResult: manualtrigger.TriggerResult{
			RunID:          102,
			ProjectID:      123,
			MRIID:          46,
			HeadSHA:        "head-sha-456",
			IdempotencyKey: "idem-456",
		},
		waitResult: db.ReviewRun{
			ID:             102,
			ProjectID:      10,
			MergeRequestID: 20,
			TriggerType:    "manual",
			HeadSha:        "head-sha-456",
			Status:         "completed",
			ErrorCode:      "",
		},
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--project-id", "123", "--mr-iid", "46", "--wait", "--json"}, runtimeDeps{
		loadConfig: func(string) (*config.Config, error) { return &config.Config{}, nil },
		openDB:     func(string) (*sql.DB, error) { return nil, nil },
		newService: func(*config.Config, *sql.DB, time.Duration) manualTriggerService { return svc },
		stdout:     &stdout,
		stderr:     &stderr,
	})

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}

	var payload jsonResponse
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal stdout: %v", err)
	}

	if !payload.OK || !payload.Waited {
		t.Fatalf("payload = %+v, want ok=true waited=true", payload)
	}
	if payload.Terminal == nil || payload.Terminal.Status != "completed" {
		t.Fatalf("payload.Terminal = %#v, want completed", payload.Terminal)
	}
	if svc.waitRunID != 102 {
		t.Fatalf("svc.waitRunID = %d, want 102", svc.waitRunID)
	}
}

func TestRunWithDepsJSONErrorOutput(t *testing.T) {
	svc := &fakeManualTriggerService{
		triggerErr: errors.New("create failed"),
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--project-id", "123", "--mr-iid", "45", "--json"}, runtimeDeps{
		loadConfig: func(string) (*config.Config, error) { return &config.Config{}, nil },
		openDB:     func(string) (*sql.DB, error) { return nil, nil },
		newService: func(*config.Config, *sql.DB, time.Duration) manualTriggerService { return svc },
		stdout:     &stdout,
		stderr:     &stderr,
	})

	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var payload jsonResponse
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal stdout: %v", err)
	}

	if payload.OK {
		t.Fatalf("payload.OK = true, want false")
	}
	if payload.Error == nil || payload.Error.Stage != "create" {
		t.Fatalf("payload.Error = %#v, want stage=create", payload.Error)
	}
}

func TestRunWithDepsRejectsInvalidPollInterval(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--project-id", "123", "--mr-iid", "45", "--poll-interval", "0s"}, runtimeDeps{
		loadConfig: func(string) (*config.Config, error) { return &config.Config{}, nil },
		openDB:     func(string) (*sql.DB, error) { return nil, nil },
		newService: func(*config.Config, *sql.DB, time.Duration) manualTriggerService { return &fakeManualTriggerService{} },
		stdout:     &stdout,
		stderr:     &stderr,
	})

	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "--poll-interval must be greater than zero") {
		t.Fatalf("stderr = %q, want poll interval validation message", stderr.String())
	}
}

func TestRunWithDepsWaitErrorReturnsJSON(t *testing.T) {
	svc := &fakeManualTriggerService{
		triggerResult: manualtrigger.TriggerResult{
			RunID:          103,
			ProjectID:      123,
			MRIID:          47,
			HeadSHA:        "head-sha-789",
			IdempotencyKey: "idem-789",
		},
		waitErr: context.DeadlineExceeded,
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--project-id", "123", "--mr-iid", "47", "--wait", "--wait-timeout", "1ms", "--json"}, runtimeDeps{
		loadConfig: func(string) (*config.Config, error) { return &config.Config{}, nil },
		openDB:     func(string) (*sql.DB, error) { return nil, nil },
		newService: func(*config.Config, *sql.DB, time.Duration) manualTriggerService { return svc },
		stdout:     &stdout,
		stderr:     &stderr,
	})

	if exitCode != 1 {
		t.Fatalf("exitCode = %d, want 1", exitCode)
	}

	var payload jsonResponse
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("Unmarshal stdout: %v", err)
	}
	if payload.Error == nil || payload.Error.Stage != "wait" {
		t.Fatalf("payload.Error = %#v, want stage=wait", payload.Error)
	}
	if payload.Created == nil || payload.Created.RunID != 103 {
		t.Fatalf("payload.Created = %#v, want run_id 103", payload.Created)
	}
}

func TestJSONResponseEndsWithNewline(t *testing.T) {
	payload := jsonResponse{
		OK: true,
		Created: &createdRunJSON{
			RunID:          1,
			ProjectID:      2,
			MRIID:          3,
			HeadSHA:        "sha",
			IdempotencyKey: "idem",
		},
		Waited: false,
	}

	var buf bytes.Buffer
	if err := writeJSONResponse(&buf, payload); err != nil {
		t.Fatalf("writeJSONResponse: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Fatalf("json output = %q, want trailing newline", buf.String())
	}
}

func TestRunWithDepsPassesPollIntervalToService(t *testing.T) {
	var gotPollInterval time.Duration

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--project-id", "123", "--mr-iid", "45", "--poll-interval", "3s"}, runtimeDeps{
		loadConfig: func(string) (*config.Config, error) { return &config.Config{}, nil },
		openDB:     func(string) (*sql.DB, error) { return nil, nil },
		newService: func(_ *config.Config, _ *sql.DB, pollInterval time.Duration) manualTriggerService {
			gotPollInterval = pollInterval
			return &fakeManualTriggerService{
				triggerResult: manualtrigger.TriggerResult{
					RunID:          100,
					ProjectID:      123,
					MRIID:          45,
					HeadSHA:        "sha",
					IdempotencyKey: "idem",
				},
			}
		},
		stdout: &stdout,
		stderr: &stderr,
	})

	if exitCode != 0 {
		t.Fatalf("exitCode = %d, want 0", exitCode)
	}
	if gotPollInterval != 3*time.Second {
		t.Fatalf("gotPollInterval = %s, want 3s", gotPollInterval)
	}
}

func TestRunWithDepsRejectsInvalidWaitTimeout(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithDeps([]string{"--project-id", "123", "--mr-iid", "45", "--wait", "--wait-timeout", "0s"}, runtimeDeps{
		loadConfig: func(string) (*config.Config, error) { return &config.Config{}, nil },
		openDB:     func(string) (*sql.DB, error) { return nil, nil },
		newService: func(*config.Config, *sql.DB, time.Duration) manualTriggerService { return &fakeManualTriggerService{} },
		stdout:     &stdout,
		stderr:     &stderr,
	})

	if exitCode != 2 {
		t.Fatalf("exitCode = %d, want 2", exitCode)
	}
	if !strings.Contains(stderr.String(), "--wait-timeout must be greater than zero") {
		t.Fatalf("stderr = %q, want wait timeout validation message", stderr.String())
	}
}

func TestNewDefaultServiceReturnsFailingServiceForInvalidGitLabConfig(t *testing.T) {
	svc := newDefaultService(&config.Config{}, nil, time.Second)

	_, err := svc.Trigger(context.Background(), manualtrigger.TriggerInput{})
	if err == nil {
		t.Fatal("Trigger error = nil, want error")
	}
	if !strings.Contains(err.Error(), "configure gitlab client") {
		t.Fatalf("Trigger error = %q, want gitlab client configuration error", err.Error())
	}
}
