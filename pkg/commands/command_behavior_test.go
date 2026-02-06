package commands

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestExecuteRunInContainerWithDependenciesUsesDevPodSSHForNonDockerProvider(t *testing.T) {
	t.Parallel()

	sshProbeCalls := 0
	interactiveCalls := 0
	err := executeRunInTestWorkspace(
		context.Background(),
		"opencode",
		func(_ context.Context, name string, args ...string) (string, error) {
			if name != "devpod" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"ssh", "--help"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected ssh probe args: got %v, want %v", args, expectedArgs)
			}

			sshProbeCalls++

			return "", nil
		},
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("kubernetes", "Running")), nil
		},
		func(_ context.Context, name string, args ...string) error {
			interactiveCalls++
			if name != "devpod" {
				t.Fatalf("unexpected interactive command %q", name)
			}

			expectedArgs := []string{"ssh", "wiki", "--", "zsh", "-ic", "opencode"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected interactive args: got %v, want %v", args, expectedArgs)
			}

			return nil
		},
		func(string) ([]byte, error) {
			t.Fatal("did not expect workspace result read for non-docker provider")
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if sshProbeCalls != 1 {
		t.Fatalf("expected one ssh probe call, got %d", sshProbeCalls)
	}

	if interactiveCalls != 1 {
		t.Fatalf("expected one interactive ssh call, got %d", interactiveCalls)
	}
}

func TestReadWorkspaceStatusWithParsesJSONFromStderr(t *testing.T) {
	t.Parallel()

	status, err := readWorkspaceStatusWith(
		context.Background(),
		"/workspace/path",
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutputWithStderr("warning: noisy log before json\n" + workspaceStatusJSON("docker", "Running")), nil
		},
		0,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if status.ID != "wiki" || status.Provider != "docker" || status.State != "Running" {
		t.Fatalf("unexpected status payload: %+v", status)
	}
}

func TestWaitForWorkspaceRunningAfterLockContentionBackoff(t *testing.T) {
	t.Parallel()

	verifyCalls := 0
	var sleeps []time.Duration
	err := waitForWorkspaceRunningAfterLockContention(
		context.Background(),
		"/workspace/path",
		func(context.Context, string) (bool, error) {
			verifyCalls++
			return verifyCalls >= 3, nil
		},
		func(duration time.Duration) {
			sleeps = append(sleeps, duration)
		},
		workspaceStartupOptions{
			timeout:        5 * time.Second,
			statusInterval: 100 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if verifyCalls != 3 {
		t.Fatalf("expected three verify calls, got %d", verifyCalls)
	}

	expectedSleeps := []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}
	if !reflect.DeepEqual(sleeps, expectedSleeps) {
		t.Fatalf("unexpected backoff durations: got %v, want %v", sleeps, expectedSleeps)
	}
}

func TestResolveWorkspaceResultPathRejectsTraversalSegments(t *testing.T) {
	t.Parallel()

	_, err := resolveWorkspaceResultPath(devPodWorkspaceStatus{ID: "../wiki", Context: "default"})
	if err == nil {
		t.Fatal("expected traversal error for workspace id")
	}

	_, err = resolveWorkspaceResultPath(devPodWorkspaceStatus{ID: "wiki", Context: "../default"})
	if err == nil {
		t.Fatal("expected traversal error for workspace context")
	}
}

func TestResolveDevPodHomeWithExpandsHomeShortcut(t *testing.T) {
	t.Parallel()

	home, err := resolveDevPodHomeWith(
		func(string) (string, bool) {
			return "~/devpod-home", true
		},
		func() (string, error) {
			return "/home/tester", nil
		},
		func(path string) (string, error) {
			return filepath.Clean(path), nil
		},
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if home != "/home/tester/devpod-home" {
		t.Fatalf("unexpected resolved DEVPOD_HOME: %q", home)
	}
}

func TestExitCodeForError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		expected int
	}{
		{
			name:     "usage error",
			err:      errors.New("run is required (use --run=<command>)"),
			expected: exitCodeUsageError,
		},
		{
			name:     "dependency missing",
			err:      fmt.Errorf("%w: docker missing", errDependencyMissing),
			expected: exitCodeDependencyMissing,
		},
		{
			name:     "workspace timeout",
			err:      fmt.Errorf("%w: startup timeout", errWorkspaceStartupTimeout),
			expected: exitCodeWorkspaceUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if code := ExitCodeForError(tt.err); code != tt.expected {
				t.Fatalf("unexpected exit code: got %d, want %d", code, tt.expected)
			}
		})
	}
}
