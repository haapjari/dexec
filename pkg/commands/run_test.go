package commands

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func workspaceStatusJSON(provider, state string) string {
	return fmt.Sprintf(
		`{"id":"wiki","context":"default","provider":%q,"state":%q}`,
		provider,
		state,
	)
}

func statusOutput(stdout string) commandOutput {
	return commandOutput{stdout: stdout}
}

func statusOutputWithStderr(stderr string) commandOutput {
	return commandOutput{stderr: stderr}
}

const (
	testWorkspacePath     = "/workspace/path"
	defaultTestRunCommand = "opencode"
)

// executeRunInTestWorkspace bypasses resolveWorkspacePath so tests do not
// depend on a synced .devcontainer directory in the host checkout.
func executeRunInTestWorkspace(
	ctx context.Context,
	selectedRun string,
	runCmd run,
	runStatus runWithOutput,
	runInteractive runInteractiveCmd,
	readWorkspaceFile readFile,
) error {
	return executeRunInContainerWithWorkspaceDependencies(
		ctx,
		selectedRun,
		testWorkspacePath,
		runCmd,
		runStatus,
		runInteractive,
		readWorkspaceFile,
		func(time.Duration) {},
		containerExecOptions{},
		func() bool { return true },
		func(string) (string, bool) { return "", false },
	)
}

func executeRunInTestWorkspaceWithOptions(
	ctx context.Context,
	runCmd run,
	runStatus runWithOutput,
	runInteractive runInteractiveCmd,
	readWorkspaceFile readFile,
	options containerExecOptions,
	isTTY func() bool,
	lookupEnv func(string) (string, bool),
) error {
	if isTTY == nil {
		isTTY = func() bool { return true }
	}

	if lookupEnv == nil {
		lookupEnv = func(string) (string, bool) { return "", false }
	}

	return executeRunInContainerWithWorkspaceDependencies(
		ctx,
		defaultTestRunCommand,
		testWorkspacePath,
		runCmd,
		runStatus,
		runInteractive,
		readWorkspaceFile,
		func(time.Duration) {},
		options,
		isTTY,
		lookupEnv,
	)
}

func newWorkflowDependencies(calls *[]string) runActionDependencies {
	return runActionDependencies{
		verifyDevContainerTemplate: func() error {
			*calls = append(*calls, "verifyDevContainerTemplate")
			return nil
		},
		verifyInstallation: func(application string) error {
			*calls = append(*calls, "verifyInstallation:"+application)
			return nil
		},
		resolveWorkspacePath: func() (string, error) {
			*calls = append(*calls, "resolveWorkspacePath")
			return "/workspace/wiki", nil
		},
		verifyContainerRunning: func(_ context.Context, workspacePath string) (bool, error) {
			*calls = append(*calls, "verifyContainerRunning:"+workspacePath)
			return false, nil
		},
		acquireWorkspaceLock: func(workspacePath string) (releaseWorkspaceLock, error) {
			*calls = append(*calls, "acquireWorkspaceLock:"+workspacePath)
			return func() error {
				*calls = append(*calls, "releaseWorkspaceLock:"+workspacePath)
				return nil
			}, nil
		},
		startDevPodContainer: func(
			_ context.Context,
			workspacePath string,
			_ workspaceStartupOptions,
		) error {
			*calls = append(*calls, "startDevPodContainer:"+workspacePath)
			return nil
		},
		executeRunInContainer: func(
			_ context.Context,
			selectedRun string,
			workspacePath string,
			_ containerExecOptions,
		) error {
			*calls = append(*calls, "executeRunInContainer:"+selectedRun+":"+workspacePath)
			return nil
		},
	}
}

func TestRunCommandWorkflowSuccess(t *testing.T) {
	t.Parallel()

	var calls []string
	deps := newWorkflowDependencies(&calls)

	err := runCommandWorkflow(
		context.Background(),
		"opencode",
		defaultWorkspaceStartupOptions(),
		containerExecOptions{},
		deps,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedCalls := []string{
		"verifyDevContainerTemplate",
		"verifyInstallation:docker",
		"verifyInstallation:devpod",
		"resolveWorkspacePath",
		"verifyContainerRunning:/workspace/wiki",
		"acquireWorkspaceLock:/workspace/wiki",
		"verifyContainerRunning:/workspace/wiki",
		"startDevPodContainer:/workspace/wiki",
		"releaseWorkspaceLock:/workspace/wiki",
		"executeRunInContainer:opencode:/workspace/wiki",
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected call order: got %v, want %v", calls, expectedCalls)
	}
}

func TestRunCommandWorkflowVerifyTemplateError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("template missing")
	var calls []string
	deps := newWorkflowDependencies(&calls)
	deps.verifyDevContainerTemplate = func() error {
		calls = append(calls, "verifyDevContainerTemplate")
		return expectedErr
	}

	err := runCommandWorkflow(
		context.Background(),
		"opencode",
		defaultWorkspaceStartupOptions(),
		containerExecOptions{},
		deps,
	)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	expectedCalls := []string{"verifyDevContainerTemplate"}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected call order: got %v, want %v", calls, expectedCalls)
	}
}

func TestRunCommandWorkflowVerifyDockerInstallationError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("docker missing")
	var calls []string
	deps := newWorkflowDependencies(&calls)
	deps.verifyInstallation = func(application string) error {
		calls = append(calls, "verifyInstallation:"+application)
		if application == "docker" {
			return expectedErr
		}

		return nil
	}

	err := runCommandWorkflow(
		context.Background(),
		"opencode",
		defaultWorkspaceStartupOptions(),
		containerExecOptions{},
		deps,
	)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	expectedCalls := []string{
		"verifyDevContainerTemplate",
		"verifyInstallation:docker",
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected call order: got %v, want %v", calls, expectedCalls)
	}
}

func TestRunCommandWorkflowVerifyDevPodInstallationError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("devpod missing")
	var calls []string
	deps := newWorkflowDependencies(&calls)
	deps.verifyInstallation = func(application string) error {
		calls = append(calls, "verifyInstallation:"+application)
		if application == "devpod" {
			return expectedErr
		}

		return nil
	}

	err := runCommandWorkflow(
		context.Background(),
		"opencode",
		defaultWorkspaceStartupOptions(),
		containerExecOptions{},
		deps,
	)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	expectedCalls := []string{
		"verifyDevContainerTemplate",
		"verifyInstallation:docker",
		"verifyInstallation:devpod",
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected call order: got %v, want %v", calls, expectedCalls)
	}
}

func TestRunCommandWorkflowResolveWorkspacePathError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("workspace path unavailable")
	var calls []string
	deps := newWorkflowDependencies(&calls)
	deps.resolveWorkspacePath = func() (string, error) {
		calls = append(calls, "resolveWorkspacePath")
		return "", expectedErr
	}

	err := runCommandWorkflow(
		context.Background(),
		"opencode",
		defaultWorkspaceStartupOptions(),
		containerExecOptions{},
		deps,
	)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	expectedCalls := []string{
		"verifyDevContainerTemplate",
		"verifyInstallation:docker",
		"verifyInstallation:devpod",
		"resolveWorkspacePath",
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected call order: got %v, want %v", calls, expectedCalls)
	}
}

func TestRunCommandWorkflowVerifyContainerRunningError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("docker unavailable")
	var calls []string
	deps := newWorkflowDependencies(&calls)
	deps.verifyContainerRunning = func(_ context.Context, workspacePath string) (bool, error) {
		calls = append(calls, "verifyContainerRunning:"+workspacePath)
		return false, expectedErr
	}

	err := runCommandWorkflow(
		context.Background(),
		"opencode",
		defaultWorkspaceStartupOptions(),
		containerExecOptions{},
		deps,
	)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	expectedCalls := []string{
		"verifyDevContainerTemplate",
		"verifyInstallation:docker",
		"verifyInstallation:devpod",
		"resolveWorkspacePath",
		"verifyContainerRunning:/workspace/wiki",
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected call order: got %v, want %v", calls, expectedCalls)
	}
}

func TestRunCommandWorkflowWaitsWhenStartupLockHeld(t *testing.T) {
	t.Parallel()

	var calls []string
	deps := newWorkflowDependencies(&calls)
	deps.acquireWorkspaceLock = func(workspacePath string) (releaseWorkspaceLock, error) {
		calls = append(calls, "acquireWorkspaceLock:"+workspacePath)
		return nil, fmt.Errorf("%w for %q", errWorkspaceStartupInProgress, workspacePath)
	}

	err := runCommandWorkflow(
		context.Background(),
		"opencode",
		workspaceStartupOptions{
			timeout:        2 * time.Millisecond,
			statusInterval: time.Millisecond,
		},
		containerExecOptions{},
		deps,
	)
	if !errors.Is(err, errWorkspaceStartupTimeout) {
		t.Fatalf("expected startup timeout error, got %v", err)
	}

	if len(calls) < 6 {
		t.Fatalf("expected lock contention retries, got calls: %v", calls)
	}

	if calls[0] != "verifyDevContainerTemplate" || calls[1] != "verifyInstallation:docker" {
		t.Fatalf("unexpected leading call order: %v", calls)
	}

	verifyRunningCalls := 0
	for _, call := range calls {
		if call == "verifyContainerRunning:/workspace/wiki" {
			verifyRunningCalls++
		}
	}

	if verifyRunningCalls < 2 {
		t.Fatalf("expected at least two running checks, got %d (%v)", verifyRunningCalls, calls)
	}
}

func TestRunCommandWorkflowSkipsStartWhenContainerAlreadyRunning(t *testing.T) {
	t.Parallel()

	var calls []string
	deps := newWorkflowDependencies(&calls)
	deps.verifyContainerRunning = func(_ context.Context, workspacePath string) (bool, error) {
		calls = append(calls, "verifyContainerRunning:"+workspacePath)
		return true, nil
	}

	err := runCommandWorkflow(
		context.Background(),
		"opencode",
		defaultWorkspaceStartupOptions(),
		containerExecOptions{},
		deps,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	expectedCalls := []string{
		"verifyDevContainerTemplate",
		"verifyInstallation:docker",
		"verifyInstallation:devpod",
		"resolveWorkspacePath",
		"verifyContainerRunning:/workspace/wiki",
		"executeRunInContainer:opencode:/workspace/wiki",
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected call order: got %v, want %v", calls, expectedCalls)
	}
}

func TestRunCommandWorkflowStartContainerError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("start failed")
	var calls []string
	deps := newWorkflowDependencies(&calls)
	deps.startDevPodContainer = func(
		_ context.Context,
		workspacePath string,
		_ workspaceStartupOptions,
	) error {
		calls = append(calls, "startDevPodContainer:"+workspacePath)
		return expectedErr
	}

	err := runCommandWorkflow(
		context.Background(),
		"opencode",
		defaultWorkspaceStartupOptions(),
		containerExecOptions{},
		deps,
	)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	expectedCalls := []string{
		"verifyDevContainerTemplate",
		"verifyInstallation:docker",
		"verifyInstallation:devpod",
		"resolveWorkspacePath",
		"verifyContainerRunning:/workspace/wiki",
		"acquireWorkspaceLock:/workspace/wiki",
		"verifyContainerRunning:/workspace/wiki",
		"startDevPodContainer:/workspace/wiki",
		"releaseWorkspaceLock:/workspace/wiki",
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected call order: got %v, want %v", calls, expectedCalls)
	}
}

func TestRunCommandWorkflowExecuteRunError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("execute failed")
	var calls []string
	deps := newWorkflowDependencies(&calls)
	deps.executeRunInContainer = func(
		_ context.Context,
		selectedRun string,
		workspacePath string,
		_ containerExecOptions,
	) error {
		calls = append(calls, "executeRunInContainer:"+selectedRun+":"+workspacePath)
		return expectedErr
	}

	err := runCommandWorkflow(
		context.Background(),
		"opencode",
		defaultWorkspaceStartupOptions(),
		containerExecOptions{},
		deps,
	)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	expectedCalls := []string{
		"verifyDevContainerTemplate",
		"verifyInstallation:docker",
		"verifyInstallation:devpod",
		"resolveWorkspacePath",
		"verifyContainerRunning:/workspace/wiki",
		"acquireWorkspaceLock:/workspace/wiki",
		"verifyContainerRunning:/workspace/wiki",
		"startDevPodContainer:/workspace/wiki",
		"releaseWorkspaceLock:/workspace/wiki",
		"executeRunInContainer:opencode:/workspace/wiki",
	}
	if !reflect.DeepEqual(calls, expectedCalls) {
		t.Fatalf("unexpected call order: got %v, want %v", calls, expectedCalls)
	}
}

func TestAcquireWorkspaceStartupLockFailsFastForConcurrentCalls(t *testing.T) {
	t.Parallel()

	workspacePath := filepath.Join(os.TempDir(), "dexec-lock-test", t.Name())
	if err := os.MkdirAll(workspacePath, 0o750); err != nil {
		t.Fatalf("create workspace path: %v", err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(workspacePath); err != nil {
			t.Fatalf("cleanup workspace path: %v", err)
		}
	})

	firstRelease, err := acquireWorkspaceStartupLock(workspacePath)
	if err != nil {
		t.Fatalf("expected first lock acquire to succeed, got %v", err)
	}
	t.Cleanup(func() {
		if firstRelease != nil {
			_ = firstRelease()
		}
	})

	_, err = acquireWorkspaceStartupLock(workspacePath)
	if !errors.Is(err, errWorkspaceStartupInProgress) {
		t.Fatalf("expected startup lock contention error, got %v", err)
	}

	if releaseErr := firstRelease(); releaseErr != nil {
		t.Fatalf("expected first lock release to succeed, got %v", releaseErr)
	}
	firstRelease = nil

	secondRelease, err := acquireWorkspaceStartupLock(workspacePath)
	if err != nil {
		t.Fatalf("expected second lock acquire after release to succeed, got %v", err)
	}

	if releaseErr := secondRelease(); releaseErr != nil {
		t.Fatalf("expected second lock release to succeed, got %v", releaseErr)
	}
}

func TestAcquireWorkspaceStartupLockCanonicalizesSymlinks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	workspacePath := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspacePath, 0o750); err != nil {
		t.Fatalf("create workspace path: %v", err)
	}

	symlinkPath := filepath.Join(root, "workspace-link")
	if err := os.Symlink(workspacePath, symlinkPath); err != nil {
		t.Fatalf("create workspace symlink: %v", err)
	}

	releaseLock, err := acquireWorkspaceStartupLock(workspacePath)
	if err != nil {
		t.Fatalf("expected lock acquire to succeed, got %v", err)
	}

	_, err = acquireWorkspaceStartupLock(symlinkPath)
	if !errors.Is(err, errWorkspaceStartupInProgress) {
		t.Fatalf("expected startup lock contention error, got %v", err)
	}

	if releaseErr := releaseLock(); releaseErr != nil {
		t.Fatalf("expected lock release to succeed, got %v", releaseErr)
	}
	secondRelease, err := acquireWorkspaceStartupLock(symlinkPath)
	if err != nil {
		t.Fatalf("expected lock acquire after release to succeed, got %v", err)
	}

	if releaseErr := secondRelease(); releaseErr != nil {
		t.Fatalf("expected lock release to succeed, got %v", releaseErr)
	}
}

func TestExecuteRunInContainerWithSuccess(t *testing.T) {
	t.Parallel()

	runCalls := 0
	statusCalls := 0
	readFileCalls := 0
	interactiveCalls := 0
	err := executeRunInTestWorkspace(
		context.Background(),
		"opencode",
		func(_ context.Context, name string, args ...string) (string, error) {
			runCalls++

			if name != "docker" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"exec", "--user", "vscode", "container-id", "zsh", "--version"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected probe args: got %v, want %v", args, expectedArgs)
			}

			return "", nil
		},
		func(_ context.Context, name string, args ...string) (commandOutput, error) {
			statusCalls++

			if name != "devpod" {
				t.Fatalf("unexpected command name %q", name)
			}

			if len(args) != 4 || args[0] != "status" || args[1] != "--output" || args[2] != "json" {
				t.Fatalf("unexpected status args %v", args)
			}

			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		func(_ context.Context, name string, args ...string) error {
			interactiveCalls++

			if name != "docker" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"exec", "-i", "-t", "--user", "vscode", "container-id", "zsh", "-ic", "opencode"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected args: got %v, want %v", args, expectedArgs)
			}

			return nil
		},
		func(path string) ([]byte, error) {
			readFileCalls++

			workspaceResultSuffix := filepath.Join("contexts", "default", "workspaces", "wiki", "workspace_result.json")
			if strings.HasSuffix(path, workspaceResultSuffix) {
				return []byte(`{"ContainerDetails":{"ID":"container-id"}}`), nil
			}

			devContainerSuffix := filepath.Join(".devcontainer", "devcontainer.json")
			if strings.HasSuffix(path, devContainerSuffix) {
				return []byte(`{"remoteUser":"vscode","remoteEnv":{"SHELL":"zsh"}}`), nil
			}

			t.Fatalf("unexpected read path %q", path)
			return nil, nil
		},
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runCalls != 1 {
		t.Fatalf("expected one shell probe call, got %d", runCalls)
	}

	if statusCalls != 1 {
		t.Fatalf("expected one status call, got %d", statusCalls)
	}

	if readFileCalls != 2 {
		t.Fatalf("expected two reads (result and devcontainer), got %d", readFileCalls)
	}

	if interactiveCalls != 1 {
		t.Fatalf("expected one interactive call, got %d", interactiveCalls)
	}
}

func TestExecuteRunInContainerWithRejectsEmptyRun(t *testing.T) {
	t.Parallel()

	runCalled := false
	runStatusCalled := false
	readFileCalled := false
	interactiveCalled := false
	err := executeRunInTestWorkspace(
		context.Background(),
		"   ",
		func(context.Context, string, ...string) (string, error) {
			runCalled = true
			return "", nil
		},
		func(context.Context, string, ...string) (commandOutput, error) {
			runStatusCalled = true
			return commandOutput{}, nil
		},
		func(context.Context, string, ...string) error {
			interactiveCalled = true
			return nil
		},
		func(string) ([]byte, error) {
			readFileCalled = true
			return nil, nil
		},
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if err.Error() != "run value cannot be empty" {
		t.Fatalf("unexpected error message: %q", err.Error())
	}

	if runCalled || runStatusCalled || readFileCalled || interactiveCalled {
		t.Fatal("expected dependencies not to be called")
	}
}

func TestExecuteRunInContainerWithRejectsEmptyRunViaWrapper(t *testing.T) {
	t.Parallel()

	interactiveCalled := false
	err := executeRunInContainerWith(
		context.Background(),
		"   ",
		func(context.Context, string, ...string) error {
			interactiveCalled = true
			return nil
		},
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if err.Error() != "run value cannot be empty" {
		t.Fatalf("unexpected error message: %q", err.Error())
	}

	if interactiveCalled {
		t.Fatal("did not expect interactive call")
	}
}

func TestExecuteRunInContainerWithWrapsRunError(t *testing.T) {
	t.Parallel()

	expectedErr := errors.New("process failed")
	err := executeRunInTestWorkspace(
		context.Background(),
		"opencode",
		func(context.Context, string, ...string) (string, error) {
			return "", nil
		},
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		func(context.Context, string, ...string) error {
			return expectedErr
		},
		func(string) ([]byte, error) {
			return []byte(`{"ContainerDetails":{"ID":"container-id"}}`), nil
		},
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected wrapped error %v, got %v", expectedErr, err)
	}
}

func TestExecuteRunInContainerWithDependenciesRejectsStoppedWorkspace(t *testing.T) {
	t.Parallel()

	err := executeRunInTestWorkspace(
		context.Background(),
		"opencode",
		func(context.Context, string, ...string) (string, error) {
			t.Fatal("did not expect fallback command call")
			return "", nil
		},
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Stopped")), nil
		},
		func(context.Context, string, ...string) error {
			t.Fatal("did not expect interactive command call")
			return nil
		},
		func(string) ([]byte, error) {
			t.Fatal("did not expect workspace result read")
			return nil, nil
		},
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !strings.Contains(err.Error(), "is \"Stopped\"") {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func TestExecuteRunInContainerWithDependenciesRejectsUnsupportedProvider(t *testing.T) {
	t.Parallel()

	sshProbeCalls := 0
	err := executeRunInTestWorkspace(
		context.Background(),
		"opencode",
		func(_ context.Context, name string, args ...string) (string, error) {
			if name != "devpod" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"ssh", "--help"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected args: got %v, want %v", args, expectedArgs)
			}

			sshProbeCalls++

			return "", errors.New("ssh not supported")
		},
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("kubernetes", "Running")), nil
		},
		func(context.Context, string, ...string) error {
			t.Fatal("did not expect interactive command call")
			return nil
		},
		func(string) ([]byte, error) {
			t.Fatal("did not expect workspace result read")
			return nil, nil
		},
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !strings.Contains(err.Error(), "devpod ssh is unavailable") {
		t.Fatalf("unexpected error message: %v", err)
	}

	if sshProbeCalls != 1 {
		t.Fatalf("expected one ssh probe call, got %d", sshProbeCalls)
	}
}

func TestExecuteRunInContainerWithDependenciesUsesImagePrefixFallback(t *testing.T) {
	t.Parallel()

	runCalls := 0
	statusCalls := 0
	err := executeRunInTestWorkspace(
		context.Background(),
		"opencode",
		func(_ context.Context, name string, args ...string) (string, error) {
			runCalls++

			if name != "docker" {
				t.Fatalf("unexpected command name %q", name)
			}

			if reflect.DeepEqual(
				args,
				[]string{"ps", "--filter", "label=devpod.workspace.id=wiki", "--format", "{{.ID}}"},
			) {
				return "", nil
			}

			if reflect.DeepEqual(
				args,
				[]string{"ps", "--filter", "label=devpod.id=wiki", "--format", "{{.ID}}"},
			) {
				return "", nil
			}

			if reflect.DeepEqual(
				args,
				[]string{"ps", "--filter", "label=devpod.workspace=wiki", "--format", "{{.ID}}"},
			) {
				return "", nil
			}

			if reflect.DeepEqual(args, []string{"ps", "--format", "{{.ID}}\t{{.Image}}"}) {
				return "container-fallback\tvsc-wiki-12345:devpod-abcdef", nil
			}

			expectedProbeArgs := []string{"exec", "--user", "vscode", "container-fallback", "zsh", "--version"}
			if reflect.DeepEqual(args, expectedProbeArgs) {
				return "", nil
			}

			t.Fatalf("unexpected docker args: %v", args)
			return "", nil
		},
		func(_ context.Context, name string, args ...string) (commandOutput, error) {
			statusCalls++
			if name != "devpod" {
				t.Fatalf("unexpected status command name %q", name)
			}

			if len(args) != 4 || args[0] != "status" || args[1] != "--output" || args[2] != "json" {
				t.Fatalf("unexpected status args %v", args)
			}
			if strings.TrimSpace(args[3]) == "" {
				t.Fatal("expected workspace reference")
			}

			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		func(_ context.Context, name string, args ...string) error {
			if name != "docker" {
				t.Fatalf("unexpected interactive command name %q", name)
			}

			expectedArgs := []string{"exec", "-i", "-t", "--user", "vscode", "container-fallback", "zsh", "-ic", "opencode"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected interactive args: got %v, want %v", args, expectedArgs)
			}

			return nil
		},
		func(string) ([]byte, error) {
			return nil, errors.New("workspace result is unavailable")
		},
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runCalls != 5 {
		t.Fatalf("expected five run calls (label filters, image fallback, shell probe), got %d", runCalls)
	}

	if statusCalls != 1 {
		t.Fatalf("expected one status call, got %d", statusCalls)
	}
}

func TestExecuteRunInContainerWithDependenciesDisablesTTYWithNoTTYOption(t *testing.T) {
	t.Parallel()

	err := executeRunInTestWorkspaceWithOptions(
		context.Background(),
		func(_ context.Context, name string, args ...string) (string, error) {
			if name != "docker" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"exec", "--user", "vscode", "container-id", "zsh", "--version"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected probe args: got %v, want %v", args, expectedArgs)
			}

			return "", nil
		},
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		func(_ context.Context, name string, args ...string) error {
			if name != "docker" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"exec", "-i", "--user", "vscode", "container-id", "zsh", "-ic", "opencode"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected interactive args: got %v, want %v", args, expectedArgs)
			}

			return nil
		},
		func(path string) ([]byte, error) {
			workspaceResultSuffix := filepath.Join("contexts", "default", "workspaces", "wiki", "workspace_result.json")
			if strings.HasSuffix(path, workspaceResultSuffix) {
				return []byte(`{"ContainerDetails":{"ID":"container-id"}}`), nil
			}

			devContainerSuffix := filepath.Join(".devcontainer", "devcontainer.json")
			if strings.HasSuffix(path, devContainerSuffix) {
				return []byte(`{"remoteUser":"vscode","remoteEnv":{"SHELL":"zsh"}}`), nil
			}

			t.Fatalf("unexpected read path %q", path)
			return nil, nil
		},
		containerExecOptions{noTTY: true},
		func() bool { return true },
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestExecuteRunInContainerWithDependenciesSkipsTTYWhenSessionIsNotTerminal(t *testing.T) {
	t.Parallel()

	err := executeRunInTestWorkspaceWithOptions(
		context.Background(),
		func(_ context.Context, name string, args ...string) (string, error) {
			if name != "docker" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"exec", "--user", "vscode", "container-id", "zsh", "--version"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected probe args: got %v, want %v", args, expectedArgs)
			}

			return "", nil
		},
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		func(_ context.Context, name string, args ...string) error {
			if name != "docker" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"exec", "-i", "--user", "vscode", "container-id", "zsh", "-ic", "opencode"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected interactive args: got %v, want %v", args, expectedArgs)
			}

			return nil
		},
		func(path string) ([]byte, error) {
			workspaceResultSuffix := filepath.Join("contexts", "default", "workspaces", "wiki", "workspace_result.json")
			if strings.HasSuffix(path, workspaceResultSuffix) {
				return []byte(`{"ContainerDetails":{"ID":"container-id"}}`), nil
			}

			devContainerSuffix := filepath.Join(".devcontainer", "devcontainer.json")
			if strings.HasSuffix(path, devContainerSuffix) {
				return []byte(`{"remoteUser":"vscode","remoteEnv":{"SHELL":"zsh"}}`), nil
			}

			t.Fatalf("unexpected read path %q", path)
			return nil, nil
		},
		containerExecOptions{},
		func() bool { return false },
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestExecuteRunInContainerWithDependenciesUsesDevContainerRuntimeConfig(t *testing.T) {
	t.Parallel()

	runCalls := 0
	err := executeRunInTestWorkspaceWithOptions(
		context.Background(),
		func(context.Context, string, ...string) (string, error) {
			runCalls++
			return "", nil
		},
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		func(_ context.Context, name string, args ...string) error {
			if name != "docker" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"exec", "-i", "-t", "--user", "devuser", "container-id", "/bin/bash", "-lc", "opencode"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected interactive args: got %v, want %v", args, expectedArgs)
			}

			return nil
		},
		func(path string) ([]byte, error) {
			workspaceResultSuffix := filepath.Join("contexts", "default", "workspaces", "wiki", "workspace_result.json")
			if strings.HasSuffix(path, workspaceResultSuffix) {
				return []byte(`{"ContainerDetails":{"ID":"container-id"}}`), nil
			}

			devContainerSuffix := filepath.Join(".devcontainer", "devcontainer.json")
			if strings.HasSuffix(path, devContainerSuffix) {
				return []byte(`{"remoteUser":"devuser","remoteEnv":{"SHELL":"/bin/bash"}}`), nil
			}

			t.Fatalf("unexpected read path %q", path)
			return nil, nil
		},
		containerExecOptions{},
		func() bool { return true },
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runCalls != 0 {
		t.Fatalf("expected no shell probe run calls, got %d", runCalls)
	}
}

func TestExecuteRunInContainerWithDependenciesUsesRuntimeConfigOverrides(t *testing.T) {
	t.Parallel()

	runCalls := 0
	err := executeRunInTestWorkspaceWithOptions(
		context.Background(),
		func(context.Context, string, ...string) (string, error) {
			runCalls++
			return "", nil
		},
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		func(_ context.Context, name string, args ...string) error {
			if name != "docker" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"exec", "-i", "-t", "--user", "override-user", "container-id", "/bin/sh", "-lc", "opencode"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected interactive args: got %v, want %v", args, expectedArgs)
			}

			return nil
		},
		func(path string) ([]byte, error) {
			workspaceResultSuffix := filepath.Join("contexts", "default", "workspaces", "wiki", "workspace_result.json")
			if strings.HasSuffix(path, workspaceResultSuffix) {
				return []byte(`{"ContainerDetails":{"ID":"container-id"}}`), nil
			}

			devContainerSuffix := filepath.Join(".devcontainer", "devcontainer.json")
			if strings.HasSuffix(path, devContainerSuffix) {
				return []byte(`{"remoteUser":"devuser","remoteEnv":{"SHELL":"/bin/bash"}}`), nil
			}

			t.Fatalf("unexpected read path %q", path)
			return nil, nil
		},
		containerExecOptions{},
		func() bool { return true },
		func(key string) (string, bool) {
			switch key {
			case containerUserOverrideEnvVar:
				return "override-user", true
			case containerShellOverrideEnvVar:
				return "/bin/sh", true
			default:
				return "", false
			}
		},
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if runCalls != 0 {
		t.Fatalf("expected no shell probe run calls, got %d", runCalls)
	}
}

func TestExecuteRunInContainerWithDependenciesFallsBackToShWhenZshIsMissing(t *testing.T) {
	t.Parallel()

	err := executeRunInTestWorkspaceWithOptions(
		context.Background(),
		func(_ context.Context, name string, args ...string) (string, error) {
			if name != "docker" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"exec", "--user", "vscode", "container-id", "zsh", "--version"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected probe args: got %v, want %v", args, expectedArgs)
			}

			return "", errors.New("zsh missing")
		},
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		func(_ context.Context, name string, args ...string) error {
			if name != "docker" {
				t.Fatalf("unexpected command name %q", name)
			}

			expectedArgs := []string{"exec", "-i", "-t", "--user", "vscode", "container-id", "sh", "-lc", "opencode"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected interactive args: got %v, want %v", args, expectedArgs)
			}

			return nil
		},
		func(path string) ([]byte, error) {
			workspaceResultSuffix := filepath.Join("contexts", "default", "workspaces", "wiki", "workspace_result.json")
			if strings.HasSuffix(path, workspaceResultSuffix) {
				return []byte(`{"ContainerDetails":{"ID":"container-id"}}`), nil
			}

			devContainerSuffix := filepath.Join(".devcontainer", "devcontainer.json")
			if strings.HasSuffix(path, devContainerSuffix) {
				return []byte(`{"remoteUser":"vscode","remoteEnv":{"SHELL":"zsh"}}`), nil
			}

			t.Fatalf("unexpected read path %q", path)
			return nil, nil
		},
		containerExecOptions{},
		func() bool { return true },
		func(string) (string, bool) { return "", false },
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
}

func TestProjectNameFromPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		expected  string
		expectErr bool
	}{
		{
			name:     "regular directory",
			path:     "/home/haspe/dev/wiki",
			expected: "wiki",
		},
		{
			name:     "hidden directory",
			path:     "/path/to/.devcontainer",
			expected: ".devcontainer",
		},
		{
			name:     "another hidden directory",
			path:     "/path/to/.claude",
			expected: ".claude",
		},
		{
			name:      "root path",
			path:      "/",
			expectErr: true,
		},
		{
			name:      "empty path",
			path:      "  ",
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			projectName, err := projectNameFromPath(tt.path)
			if tt.expectErr {
				if err == nil {
					t.Fatal("expected an error, got nil")
				}

				return
			}

			if err != nil {
				t.Fatalf("expected no error, got %v", err)
			}

			if projectName != tt.expected {
				t.Fatalf("unexpected project name: got %q, want %q", projectName, tt.expected)
			}
		})
	}
}

func TestResolveProjectNameWithGetwd(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		projectName, err := resolveProjectNameWith(func() (string, error) {
			return "/home/haspe/dev/.claude", nil
		})
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if projectName != ".claude" {
			t.Fatalf("unexpected project name: got %q, want %q", projectName, ".claude")
		}
	})

	t.Run("getwd failure", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("pwd failed")
		_, err := resolveProjectNameWith(func() (string, error) {
			return "", expectedErr
		})
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected wrapped error %v, got %v", expectedErr, err)
		}
	})
}

func TestVerifyContainerRunningWith(t *testing.T) {
	t.Parallel()
	workspacePath := filepath.Clean(os.TempDir())

	t.Run("running workspace returns true", func(t *testing.T) {
		t.Parallel()

		running, err := verifyContainerRunningWith(
			context.Background(),
			workspacePath,
			func(_ context.Context, name string, args ...string) (commandOutput, error) {
				if name != "devpod" {
					t.Fatalf("unexpected command name %q", name)
				}

				expectedArgs := []string{"status", "--output", "json", workspacePath}
				if !reflect.DeepEqual(args, expectedArgs) {
					t.Fatalf("unexpected args: got %v, want %v", args, expectedArgs)
				}

				return statusOutput(workspaceStatusJSON("docker", "Running")), nil
			},
			func() (string, error) {
				return workspacePath, nil
			},
		)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if !running {
			t.Fatal("expected container to be reported as running")
		}
	})

	t.Run("stopped workspace returns false", func(t *testing.T) {
		t.Parallel()

		running, err := verifyContainerRunningWith(
			context.Background(),
			workspacePath,
			func(context.Context, string, ...string) (commandOutput, error) {
				return statusOutput(workspaceStatusJSON("docker", "Stopped")), nil
			},
			func() (string, error) {
				return workspacePath, nil
			},
		)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if running {
			t.Fatal("expected container to be reported as not running")
		}
	})

	t.Run("workspace not found returns false", func(t *testing.T) {
		t.Parallel()

		running, err := verifyContainerRunningWith(
			context.Background(),
			workspacePath,
			func(context.Context, string, ...string) (commandOutput, error) {
				return statusOutputWithStderr("workspace doesn't exist"), errors.New("workspace doesn't exist")
			},
			func() (string, error) {
				return workspacePath, nil
			},
		)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if running {
			t.Fatal("expected container to be reported as not running")
		}
	})

	t.Run("workspace name falls back to current path", func(t *testing.T) {
		t.Parallel()
		calls := 0

		running, err := verifyContainerRunningWith(
			context.Background(),
			"wiki",
			func(_ context.Context, name string, args ...string) (commandOutput, error) {
				calls++

				if name != "devpod" {
					t.Fatalf("unexpected command name %q", name)
				}

				workspaceRef := args[3]
				if workspaceRef == "wiki" {
					return statusOutputWithStderr("workspace wiki doesn't exist"), errors.New("workspace wiki doesn't exist")
				}

				if workspaceRef != workspacePath {
					t.Fatalf("unexpected fallback workspace ref %q", workspaceRef)
				}

				return statusOutput(workspaceStatusJSON("docker", "Running")), nil
			},
			func() (string, error) {
				return workspacePath, nil
			},
		)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if !running {
			t.Fatal("expected container to be reported as running")
		}

		if calls != 2 {
			t.Fatalf("expected two status calls, got %d", calls)
		}
	})

	t.Run("status command failure returns error", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("status failed")
		_, err := verifyContainerRunningWith(
			context.Background(),
			workspacePath,
			func(context.Context, string, ...string) (commandOutput, error) {
				return commandOutput{}, expectedErr
			},
			func() (string, error) {
				return workspacePath, nil
			},
		)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected wrapped error %v, got %v", expectedErr, err)
		}
	})
}

func TestReadWorkspaceStatusWith(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		status, err := readWorkspaceStatusWith(
			context.Background(),
			"/workspace/path",
			func(context.Context, string, ...string) (commandOutput, error) {
				return statusOutput(workspaceStatusJSON("docker", "Running")), nil
			},
			0,
		)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if status.ID != "wiki" || status.Provider != "docker" || status.State != "Running" {
			t.Fatalf("unexpected status payload: %+v", status)
		}
	})

	t.Run("workspace not found maps to sentinel", func(t *testing.T) {
		t.Parallel()

		_, err := readWorkspaceStatusWith(
			context.Background(),
			"/workspace/path",
			func(context.Context, string, ...string) (commandOutput, error) {
				return statusOutputWithStderr("workspace /workspace/path doesn't exist"), errors.New("workspace /workspace/path doesn't exist")
			},
			0,
		)
		if !errors.Is(err, errWorkspaceNotFound) {
			t.Fatalf("expected errWorkspaceNotFound, got %v", err)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		t.Parallel()

		_, err := readWorkspaceStatusWith(
			context.Background(),
			"/workspace/path",
			func(context.Context, string, ...string) (commandOutput, error) {
				return statusOutput("not-json"), nil
			},
			0,
		)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if !strings.Contains(err.Error(), "parse workspace status") {
			t.Fatalf("unexpected parse error: %v", err)
		}
	})

	t.Run("missing required status fields", func(t *testing.T) {
		t.Parallel()

		_, err := readWorkspaceStatusWith(
			context.Background(),
			"/workspace/path",
			func(context.Context, string, ...string) (commandOutput, error) {
				return statusOutput(`{"id":"wiki"}`), nil
			},
			0,
		)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if !strings.Contains(err.Error(), "missing state") {
			t.Fatalf("unexpected missing-state error: %v", err)
		}
	})
}

func TestResolveWorkspacePathWithFindsWorkspaceRoot(t *testing.T) {
	t.Parallel()

	workspaceRoot := filepath.Clean(os.TempDir())
	workspacePath := filepath.Join(workspaceRoot, "nested", "project")
	statCalls := 0
	resolvedPath, err := resolveWorkspacePathWith(
		func() (string, error) {
			return workspacePath, nil
		},
		func(path string) (string, error) {
			return path, nil
		},
		func(path string) (string, error) {
			return path, nil
		},
		func(path string) (os.FileInfo, error) {
			statCalls++
			if path == filepath.Join(workspaceRoot, ".devcontainer", "devcontainer.json") {
				return fakeFileInfo{name: "devcontainer.json"}, nil
			}
			return nil, os.ErrNotExist
		},
	)
	if err != nil {
		t.Fatalf("expected workspace root, got error %v", err)
	}
	if resolvedPath != workspaceRoot {
		t.Fatalf("unexpected workspace root: got %q, want %q", resolvedPath, workspaceRoot)
	}
	if statCalls == 0 {
		t.Fatal("expected stat to be called")
	}
}

func TestResolveWorkspacePathWithReturnsErrorWhenTemplateIsMissing(t *testing.T) {
	t.Parallel()

	_, err := resolveWorkspacePathWith(
		func() (string, error) {
			return filepath.Clean(os.TempDir()), nil
		},
		func(path string) (string, error) {
			return path, nil
		},
		func(path string) (string, error) {
			return path, nil
		},
		func(string) (os.FileInfo, error) {
			return nil, os.ErrNotExist
		},
	)
	if err == nil {
		t.Fatal("expected error for missing devcontainer.json")
	}
}

func TestCanonicalizeWorkspacePathRejectsEmptyPath(t *testing.T) {
	t.Parallel()

	_, err := canonicalizeWorkspacePath(
		"  ",
		func(string) (string, error) {
			return "", nil
		},
		func(string) (string, error) {
			return "", nil
		},
	)
	if err == nil {
		t.Fatal("expected error for empty workspace path")
	}
}

func TestWorkspaceStartupLockPathUsesCanonicalPath(t *testing.T) {
	t.Parallel()
	lockPath := workspaceStartupLockPath(filepath.Clean(os.TempDir()))
	if lockPath == "" {
		t.Fatal("expected non-empty lock path")
	}
	if !strings.HasPrefix(lockPath, os.TempDir()) {
		t.Fatalf("expected lock path in temp dir, got %q", lockPath)
	}
}

type fakeFileInfo struct {
	name string
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() os.FileMode  { return 0 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return false }
func (f fakeFileInfo) Sys() any           { return nil }

func TestStartDevPodContainerWith(t *testing.T) {
	t.Parallel()

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		runCalls := 0
		statusCalls := 0
		err := startDevPodContainerWith(
			context.Background(),
			"/workspace/path",
			func(context.Context, string, ...string) (string, error) {
				runCalls++
				return "", nil
			},
			func(context.Context, string, ...string) (commandOutput, error) {
				statusCalls++
				return statusOutput(workspaceStatusJSON("docker", "Running")), nil
			},
			defaultWorkspaceStartupOptions(),
		)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if runCalls != 1 {
			t.Fatalf("expected one run call, got %d", runCalls)
		}

		if statusCalls != 1 {
			t.Fatalf("expected one status call, got %d", statusCalls)
		}
	})

	t.Run("empty workspace path", func(t *testing.T) {
		t.Parallel()

		err := startDevPodContainerWith(
			context.Background(),
			"   ",
			func(context.Context, string, ...string) (string, error) {
				t.Fatal("did not expect run call")
				return "", nil
			},
			func(context.Context, string, ...string) (commandOutput, error) {
				t.Fatal("did not expect status call")
				return commandOutput{}, nil
			},
			defaultWorkspaceStartupOptions(),
		)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}
	})

	t.Run("up command failure", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("up failed")
		err := startDevPodContainerWith(
			context.Background(),
			"/workspace/path",
			func(context.Context, string, ...string) (string, error) {
				return "", expectedErr
			},
			func(context.Context, string, ...string) (commandOutput, error) {
				return commandOutput{}, expectedErr
			},
			defaultWorkspaceStartupOptions(),
		)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if !errors.Is(err, expectedErr) {
			t.Fatalf("expected wrapped error %v, got %v", expectedErr, err)
		}
	})
}

func TestResolveWorkspaceContainerIDByImagePrefixWith(t *testing.T) {
	t.Parallel()

	t.Run("single match", func(t *testing.T) {
		t.Parallel()

		containerID, err := resolveWorkspaceContainerIDByImagePrefixWith(
			context.Background(),
			"wiki",
			func(context.Context, string, ...string) (string, error) {
				return "abc123\tvsc-wiki-123:devpod-abc\ndef456\tvsc-other-123:devpod-def", nil
			},
		)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if containerID != "abc123" {
			t.Fatalf("unexpected container id %q", containerID)
		}
	})

	t.Run("multiple matches", func(t *testing.T) {
		t.Parallel()

		_, err := resolveWorkspaceContainerIDByImagePrefixWith(
			context.Background(),
			"wiki",
			func(context.Context, string, ...string) (string, error) {
				return "abc\tvsc-wiki-123\ndef\tvsc-wiki-456", nil
			},
		)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if !strings.Contains(err.Error(), "multiple workspace containers found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("no matches", func(t *testing.T) {
		t.Parallel()

		_, err := resolveWorkspaceContainerIDByImagePrefixWith(
			context.Background(),
			"wiki",
			func(context.Context, string, ...string) (string, error) {
				return "abc\tvsc-other-123", nil
			},
		)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if !strings.Contains(err.Error(), "workspace container not found") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestResolveWorkspaceContainerIDWith(t *testing.T) {
	t.Parallel()

	status := devPodWorkspaceStatus{ID: "wiki", Context: "default"}

	t.Run("reads from workspace result file", func(t *testing.T) {
		t.Parallel()

		containerID, err := resolveWorkspaceContainerIDWith(
			context.Background(),
			status,
			func(string) ([]byte, error) {
				return []byte(`{"ContainerDetails":{"ID":"container-file"}}`), nil
			},
			func(context.Context, string, ...string) (string, error) {
				t.Fatal("did not expect fallback command call")
				return "", nil
			},
		)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if containerID != "container-file" {
			t.Fatalf("unexpected container id %q", containerID)
		}
	})

	t.Run("falls back to image prefix", func(t *testing.T) {
		t.Parallel()

		containerID, err := resolveWorkspaceContainerIDWith(
			context.Background(),
			status,
			func(string) ([]byte, error) {
				return nil, errors.New("workspace result missing")
			},
			func(_ context.Context, name string, args ...string) (string, error) {
				if name != "docker" {
					t.Fatalf("unexpected command name %q", name)
				}

				if reflect.DeepEqual(
					args,
					[]string{"ps", "--filter", "label=devpod.workspace.id=wiki", "--format", "{{.ID}}"},
				) {
					return "", nil
				}

				if reflect.DeepEqual(
					args,
					[]string{"ps", "--filter", "label=devpod.id=wiki", "--format", "{{.ID}}"},
				) {
					return "", nil
				}

				if reflect.DeepEqual(
					args,
					[]string{"ps", "--filter", "label=devpod.workspace=wiki", "--format", "{{.ID}}"},
				) {
					return "", nil
				}

				if reflect.DeepEqual(args, []string{"ps", "--format", "{{.ID}}\t{{.Image}}"}) {
					return "container-fallback\tvsc-wiki-12345", nil
				}

				t.Fatalf("unexpected docker args: %v", args)

				return "", nil
			},
		)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if containerID != "container-fallback" {
			t.Fatalf("unexpected container id %q", containerID)
		}
	})

	t.Run("retries workspace result before fallback", func(t *testing.T) {
		t.Parallel()

		readAttempts := 0
		containerID, err := resolveWorkspaceContainerIDWithRetry(
			context.Background(),
			status,
			func(string) ([]byte, error) {
				readAttempts++
				if readAttempts < 3 {
					return nil, errors.New("workspace result missing")
				}

				return []byte(`{"ContainerDetails":{"ID":"container-after-retry"}}`), nil
			},
			func(context.Context, string, ...string) (string, error) {
				t.Fatal("did not expect fallback command call")
				return "", nil
			},
			func(time.Duration) {},
			4,
			10*time.Millisecond,
		)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}

		if containerID != "container-after-retry" {
			t.Fatalf("unexpected container id %q", containerID)
		}

		if readAttempts != 3 {
			t.Fatalf("expected three workspace result attempts, got %d", readAttempts)
		}
	})

	t.Run("returns joined error when all strategies fail", func(t *testing.T) {
		t.Parallel()

		_, err := resolveWorkspaceContainerIDWith(
			context.Background(),
			status,
			func(string) ([]byte, error) {
				return nil, errors.New("workspace result missing")
			},
			func(context.Context, string, ...string) (string, error) {
				return "", nil
			},
		)
		if err == nil {
			t.Fatal("expected an error, got nil")
		}

		if !strings.Contains(err.Error(), "resolve container id") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestVerifyContainerHealthWithHealthyImmediately(t *testing.T) {
	t.Parallel()

	attempts := 0
	sleeps := 0
	err := verifyContainerHealthWith(
		context.Background(),
		"/workspace/path",
		func(_ context.Context, name string, args ...string) (commandOutput, error) {
			attempts++
			if name != "devpod" {
				t.Fatalf("unexpected command %q", name)
			}

			expectedArgs := []string{"status", "--output", "json", "/workspace/path"}
			if !reflect.DeepEqual(args, expectedArgs) {
				t.Fatalf("unexpected command args: got %v, want %v", args, expectedArgs)
			}

			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		func(time.Duration) {
			sleeps++
		},
		50*time.Millisecond,
		10*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if attempts != 1 {
		t.Fatalf("expected one status attempt, got %d", attempts)
	}

	if sleeps != 0 {
		t.Fatalf("expected no sleeps, got %d", sleeps)
	}
}

func TestVerifyContainerHealthWithBecomesHealthyAfterRetries(t *testing.T) {
	t.Parallel()

	states := []string{"Starting", "Initializing", "Running"}
	attempts := 0
	sleeps := 0
	err := verifyContainerHealthWith(
		context.Background(),
		"/workspace/path",
		func(context.Context, string, ...string) (commandOutput, error) {
			if attempts >= len(states) {
				t.Fatal("run called more times than expected")
			}

			state := states[attempts]
			attempts++
			return statusOutput(workspaceStatusJSON("docker", state)), nil
		},
		func(time.Duration) {
			sleeps++
		},
		30*time.Millisecond,
		10*time.Millisecond,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if attempts != 3 {
		t.Fatalf("expected three status attempts, got %d", attempts)
	}

	if sleeps != 2 {
		t.Fatalf("expected two sleeps, got %d", sleeps)
	}
}

func TestVerifyContainerHealthWithUnhealthyStatusReturnsSentinel(t *testing.T) {
	t.Parallel()

	err := verifyContainerHealthWith(
		context.Background(),
		"/workspace/path",
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Stopped")), nil
		},
		func(time.Duration) {},
		50*time.Millisecond,
		10*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !errors.Is(err, errContainerUnhealthy) {
		t.Fatalf("expected errContainerUnhealthy, got %v", err)
	}
}

func TestVerifyContainerHealthWithContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	attempts := 0
	err := verifyContainerHealthWith(
		ctx,
		"/workspace/path",
		func(context.Context, string, ...string) (commandOutput, error) {
			attempts++
			return statusOutput(workspaceStatusJSON("docker", "Starting")), nil
		},
		func(time.Duration) {
			cancel()
		},
		50*time.Millisecond,
		10*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}

	if attempts != 1 {
		t.Fatalf("expected one attempt before cancellation, got %d", attempts)
	}
}

func TestVerifyContainerHealthWithInvalidAttempts(t *testing.T) {
	t.Parallel()

	err := verifyContainerHealthWith(
		context.Background(),
		"/workspace/path",
		func(context.Context, string, ...string) (commandOutput, error) {
			return commandOutput{}, nil
		},
		func(time.Duration) {},
		0,
		10*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if err.Error() != "startup-timeout must be greater than zero" {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

func TestVerifyContainerHealthWithRetriesExhausted(t *testing.T) {
	t.Parallel()

	err := verifyContainerHealthWith(
		context.Background(),
		"/workspace/path",
		func(context.Context, string, ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Initializing")), nil
		},
		func(time.Duration) {},
		20*time.Millisecond,
		10*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !errors.Is(err, errWorkspaceStartupTimeout) {
		t.Fatalf("unexpected timeout error: %v", err)
	}
}

func TestVerifyContainerHealthWithCommandErrorEventuallyReturnsTimeout(t *testing.T) {
	t.Parallel()

	err := verifyContainerHealthWith(
		context.Background(),
		"/workspace/path",
		func(context.Context, string, ...string) (commandOutput, error) {
			return commandOutput{}, errors.New("status command failed")
		},
		func(time.Duration) {},
		20*time.Millisecond,
		10*time.Millisecond,
	)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}

	if !strings.Contains(err.Error(), "read workspace status") {
		t.Fatalf("expected contextual status error, got %v", err)
	}
}

func TestReadWorkspaceStatusWithTimesOutOnSlowCommand(t *testing.T) {
	t.Parallel()

	blocked := make(chan struct{})

	status, err := readWorkspaceStatusWith(
		context.Background(),
		"/workspace/path",
		func(ctx context.Context, _ string, _ ...string) (commandOutput, error) {
			select {
			case <-ctx.Done():
				return commandOutput{}, ctx.Err()
			case <-blocked:
				return statusOutput(workspaceStatusJSON("docker", "Running")), nil
			}
		},
		50*time.Millisecond,
	)
	close(blocked)

	if err == nil {
		t.Fatalf("expected timeout error, got status %+v", status)
	}

	if !errors.Is(err, errDevPodStatusCommandTimeout) {
		t.Fatalf("expected errDevPodStatusCommandTimeout, got %v", err)
	}

	if !strings.Contains(err.Error(), "50ms") {
		t.Fatalf("expected timeout duration in error message, got %v", err)
	}
}

func TestReadWorkspaceStatusWithPropagatesParentCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())

	_, err := readWorkspaceStatusWith(
		ctx,
		"/workspace/path",
		func(innerCtx context.Context, _ string, _ ...string) (commandOutput, error) {
			cancel()
			<-innerCtx.Done()
			return commandOutput{}, innerCtx.Err()
		},
		5*time.Second,
	)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestReadWorkspaceStatusWithZeroTimeoutSkipsDeadline(t *testing.T) {
	t.Parallel()

	status, err := readWorkspaceStatusWith(
		context.Background(),
		"/workspace/path",
		func(_ context.Context, _ string, _ ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		0,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if status.State != "Running" {
		t.Fatalf("expected Running state, got %q", status.State)
	}
}

func TestStartDevPodContainerWithTimesOutOnSlowUp(t *testing.T) {
	t.Parallel()

	blocked := make(chan struct{})
	runCalled := false

	err := startDevPodContainerWith(
		context.Background(),
		"/workspace/path",
		func(ctx context.Context, name string, args ...string) (string, error) {
			if name == "devpod" && len(args) > 0 && args[0] == "up" {
				runCalled = true
				select {
				case <-ctx.Done():
					return "", ctx.Err()
				case <-blocked:
					return "", nil
				}
			}

			return "", nil
		},
		func(_ context.Context, _ string, _ ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		workspaceStartupOptions{
			timeout:        50 * time.Millisecond,
			statusInterval: 10 * time.Millisecond,
		},
	)
	close(blocked)

	if !runCalled {
		t.Fatal("expected devpod up to be called")
	}

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout message, got %v", err)
	}
}

func TestVerifyContainerHealthWithHandlesPerCallStatusTimeout(t *testing.T) {
	t.Parallel()

	attempts := 0
	err := verifyContainerHealthWith(
		context.Background(),
		"/workspace/path",
		func(_ context.Context, _ string, _ ...string) (commandOutput, error) {
			attempts++
			if attempts <= 2 {
				return commandOutput{}, fmt.Errorf(
					"%w: exceeded 30s timeout",
					errDevPodStatusCommandTimeout,
				)
			}

			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		func(time.Duration) {},
		50*time.Millisecond,
		10*time.Millisecond,
	)

	if err != nil {
		t.Fatalf("expected no error after retry, got %v", err)
	}

	if attempts < 3 {
		t.Fatalf("expected at least 3 attempts, got %d", attempts)
	}
}

func TestWorkspaceRunningForReferenceTimesOutOnSlowStatus(t *testing.T) {
	t.Parallel()

	blocked := make(chan struct{})

	// Call readWorkspaceStatusWith directly with a short timeout instead of
	// going through workspaceRunningForReference, which hardcodes the 30s
	// production timeout. This tests the same code path without the wait.
	_, err := readWorkspaceStatusWith(
		context.Background(),
		"/workspace/path",
		func(ctx context.Context, _ string, _ ...string) (commandOutput, error) {
			select {
			case <-ctx.Done():
				return commandOutput{}, ctx.Err()
			case <-blocked:
				return statusOutput(workspaceStatusJSON("docker", "Running")), nil
			}
		},
		50*time.Millisecond,
	)
	close(blocked)

	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}

	if !errors.Is(err, errDevPodStatusCommandTimeout) {
		t.Fatalf("expected errDevPodStatusCommandTimeout, got %v", err)
	}
}

func TestEnsureWorkspaceRunningDoubleCheckAfterLockFindsRunning(t *testing.T) {
	t.Parallel()

	verifyCalls := 0
	startCalled := false

	err := ensureWorkspaceRunning(
		context.Background(),
		"/workspace/path",
		func(_ context.Context, _ string) (bool, error) {
			verifyCalls++
			if verifyCalls == 1 {
				return false, nil
			}

			return true, nil
		},
		func(_ string) (releaseWorkspaceLock, error) {
			return func() error { return nil }, nil
		},
		func(_ context.Context, _ string, _ workspaceStartupOptions) error {
			startCalled = true
			return nil
		},
		func(time.Duration) {},
		defaultWorkspaceStartupOptions(),
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if verifyCalls != 2 {
		t.Fatalf("expected 2 verify calls (pre-lock + post-lock), got %d", verifyCalls)
	}

	if startCalled {
		t.Fatal("expected startDevPodContainer to NOT be called when double-check finds running")
	}
}

func TestWaitForWorkspaceRunningAfterLockContentionRespectsContextDeadline(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	err := waitForWorkspaceRunningAfterLockContention(
		ctx,
		"/workspace/path",
		func(_ context.Context, _ string) (bool, error) {
			return false, nil
		},
		func(time.Duration) {},
		workspaceStartupOptions{
			timeout:        10 * time.Second,
			statusInterval: 1 * time.Millisecond,
		},
	)

	if err == nil {
		t.Fatal("expected error from context deadline, got nil")
	}

	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, errWorkspaceStartupTimeout) {
		t.Fatalf("expected context deadline or startup timeout error, got %v", err)
	}
}

func TestWaitForWorkspaceRunningAfterLockContentionWithStatusErrors(t *testing.T) {
	t.Parallel()

	verifyCalls := 0

	err := waitForWorkspaceRunningAfterLockContention(
		context.Background(),
		"/workspace/path",
		func(_ context.Context, _ string) (bool, error) {
			verifyCalls++
			if verifyCalls <= 2 {
				return false, fmt.Errorf("transient status error %d", verifyCalls)
			}

			return true, nil
		},
		func(time.Duration) {},
		workspaceStartupOptions{
			timeout:        5 * time.Second,
			statusInterval: 1 * time.Millisecond,
		},
	)
	if err != nil {
		t.Fatalf("expected nil after recovering from transient errors, got %v", err)
	}

	if verifyCalls != 3 {
		t.Fatalf("expected 3 verify calls, got %d", verifyCalls)
	}
}

func TestWaitForWorkspaceRunningAfterLockContentionTimesOutWithLastError(t *testing.T) {
	t.Parallel()

	err := waitForWorkspaceRunningAfterLockContention(
		context.Background(),
		"/workspace/path",
		func(_ context.Context, _ string) (bool, error) {
			return false, errors.New("connection refused")
		},
		func(time.Duration) {},
		workspaceStartupOptions{
			timeout:        2 * time.Millisecond,
			statusInterval: 1 * time.Millisecond,
		},
	)

	if !errors.Is(err, errWorkspaceStartupTimeout) {
		t.Fatalf("expected errWorkspaceStartupTimeout, got %v", err)
	}

	if !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("expected last error in timeout message, got %v", err)
	}
}

func TestReadWorkspaceStatusWithTimeoutDoesNotAffectSuccessfulCall(t *testing.T) {
	t.Parallel()

	status, err := readWorkspaceStatusWith(
		context.Background(),
		"/workspace/path",
		func(_ context.Context, _ string, _ ...string) (commandOutput, error) {
			return statusOutput(workspaceStatusJSON("docker", "Running")), nil
		},
		5*time.Second,
	)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}

	if status.State != "Running" || status.Provider != "docker" {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestStartDevPodContainerWithParentContextCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := startDevPodContainerWith(
		ctx,
		"/workspace/path",
		func(ctx context.Context, _ string, _ ...string) (string, error) {
			return "", ctx.Err()
		},
		func(_ context.Context, _ string, _ ...string) (commandOutput, error) {
			return commandOutput{}, nil
		},
		workspaceStartupOptions{
			timeout:        60 * time.Second,
			statusInterval: 500 * time.Millisecond,
		},
	)

	if err == nil {
		t.Fatal("expected error from canceled context, got nil")
	}
}
