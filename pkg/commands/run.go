// Package commands defines command-line command constructors and action handlers
// used by the executable.
//
// It wires command names to actions that validate local prerequisites and
// prepare command execution inside a development container.
package commands

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	cli "github.com/urfave/cli/v3"
)

const (
	runFlagName                        = "run"
	runFlagAlias                       = "r"
	noTTYFlagName                      = "no-tty"
	startupTimeoutFlagName             = "startup-timeout"
	statusIntervalFlagName             = "status-interval"
	debugEnvVar                        = "DEXEC_DEBUG"
	containerUserOverrideEnvVar        = "DEXEC_CONTAINER_USER"
	containerShellOverrideEnvVar       = "DEXEC_CONTAINER_SHELL"
	defaultContainerUser               = "vscode"
	defaultContainerShell              = "zsh"
	fallbackContainerShell             = "sh"
	devPodStatusAction                 = "status"
	devPodSSHAction                    = "ssh"
	devContainerDirectoryName          = ".devcontainer"
	devContainerTemplateFileName       = "devcontainer.json"
	workspaceResultFileName            = "workspace_result.json"
	defaultWorkspaceContextName        = "default"
	workspaceStartupLockFileNamePrefix = "dexec-workspace-startup"
	defaultWorkspaceStartupTimeout     = 60 * time.Second
	defaultWorkspaceStatusInterval     = 500 * time.Millisecond
	maxWorkspaceStartupBackoffInterval = 5 * time.Second
	containerIDMaxAttempts             = 6
	containerIDAttemptInterval         = 200 * time.Millisecond
	devPodStatusCommandTimeout         = 30 * time.Second
)

var dockerWorkspaceLabelKeys = []string{
	"devpod.workspace.id",
	"devpod.id",
	"devpod.workspace",
}

type (
	commandOutput struct {
		stdout string
		stderr string
	}
	run                  func(ctx context.Context, name string, args ...string) (string, error)
	runWithOutput        func(ctx context.Context, name string, args ...string) (commandOutput, error)
	runInteractiveCmd    func(ctx context.Context, name string, args ...string) error
	readFile             func(string) ([]byte, error)
	sleep                func(time.Duration)
	releaseWorkspaceLock func() error
	containerExecOptions struct {
		noTTY bool
	}
	workspaceStartupOptions struct {
		timeout        time.Duration
		statusInterval time.Duration
	}
	containerExecRuntimeConfig struct {
		user  string
		shell string
	}
	devContainerTemplate struct {
		RemoteUser    string         `json:"remoteUser"`
		ContainerUser string         `json:"containerUser"`
		RemoteEnv     map[string]any `json:"remoteEnv"`
	}
	devPodWorkspaceStatus struct {
		ID       string `json:"id"`
		Context  string `json:"context"`
		Provider string `json:"provider"`
		State    string `json:"state"`
	}
	devPodWorkspaceResult struct {
		ContainerDetails *devPodContainerDetails `json:"ContainerDetails"`
	}
	devPodContainerDetails struct {
		ID string `json:"ID"`
	}
	runActionDependencies struct {
		verifyDevContainerTemplate func() error
		verifyInstallation         func(string) error
		resolveWorkspacePath       func() (string, error)
		verifyContainerRunning     func(context.Context, string) (bool, error)
		acquireWorkspaceLock       func(string) (releaseWorkspaceLock, error)
		startDevPodContainer       func(context.Context, string, workspaceStartupOptions) error
		executeRunInContainer      func(context.Context, string, string, containerExecOptions) error
	}
)

var (
	errContainerUnhealthy          = errors.New("container is unhealthy")
	errWorkspaceNotFound           = errors.New("workspace does not exist")
	errWorkspaceStartupInProgress  = errors.New("workspace startup is already in progress")
	errWorkspaceStartupTimeout     = errors.New("workspace startup timed out")
	errWorkspaceContainerNotFound  = errors.New("workspace container not found")
	errMultipleWorkspaceContainers = errors.New("multiple workspace containers found")
	errDependencyMissing           = errors.New("required dependency is missing")
	errDevPodStatusCommandTimeout  = errors.New("devpod status command timed out")
)

// RunCommand builds the `run` command-line command.
//
// It wires the command name and action used to perform preflight checks and
// prepare execution in the development container.
func RunCommand() *cli.Command {
	return &cli.Command{
		Name:   runFlagName,
		Flags:  []cli.Flag{RunFlag(), NoTTYFlag(), StartupTimeoutFlag(), StatusIntervalFlag()},
		Action: RunAction(),
	}
}

// RunFlag builds the command selection flag.
func RunFlag() *cli.StringFlag {
	return &cli.StringFlag{
		Name:    runFlagName,
		Aliases: []string{runFlagAlias},
		Usage:   "command to execute",
	}
}

// NoTTYFlag builds the pseudo-TTY toggle flag.
func NoTTYFlag() *cli.BoolFlag {
	return &cli.BoolFlag{
		Name:  noTTYFlagName,
		Usage: "disable pseudo-TTY allocation for docker exec",
	}
}

// StartupTimeoutFlag builds the workspace startup timeout flag.
func StartupTimeoutFlag() *cli.DurationFlag {
	return &cli.DurationFlag{
		Name:  startupTimeoutFlagName,
		Usage: "maximum time to wait for workspace startup",
		Value: defaultWorkspaceStartupTimeout,
	}
}

// StatusIntervalFlag builds the workspace status poll interval flag.
func StatusIntervalFlag() *cli.DurationFlag {
	return &cli.DurationFlag{
		Name:  statusIntervalFlagName,
		Usage: "poll interval for workspace status checks",
		Value: defaultWorkspaceStatusInterval,
	}
}

// RunRootAction runs the command workflow from the root command.
func RunRootAction() cli.ActionFunc {
	runAction := RunAction()

	return func(ctx context.Context, cmd *cli.Command) error {
		if !hasRunSelection(cmd.String(runFlagName), cmd.IsSet(runFlagName), cmd.Args().Slice()) {
			return cli.ShowRootCommandHelp(cmd)
		}

		return runAction(ctx, cmd)
	}
}

// RunAction returns the action for the `run` command.
//
// The action validates the devcontainer template, checks required local
// dependencies, and starts the container before continuing with command
// execution.
func RunAction() cli.ActionFunc {
	return runActionWith(defaultRunActionDependencies())
}

func defaultRunActionDependencies() runActionDependencies {
	return runActionDependencies{
		verifyDevContainerTemplate: verifyDevContainerTemplate,
		verifyInstallation:         verifyInstallation,
		resolveWorkspacePath:       resolveWorkspacePath,
		verifyContainerRunning:     verifyContainerRunning,
		acquireWorkspaceLock:       acquireWorkspaceStartupLock,
		startDevPodContainer:       startDevPodContainer,
		executeRunInContainer:      executeRunInContainer,
	}
}

func runActionWith(deps runActionDependencies) cli.ActionFunc {
	return func(ctx context.Context, cmd *cli.Command) error {
		selectedRun, err := resolveRunSelection(
			cmd.String(runFlagName),
			cmd.IsSet(runFlagName),
			cmd.Args().Slice(),
		)
		if err != nil {
			return err
		}

		startupOptions, err := resolveWorkspaceStartupOptions(cmd)
		if err != nil {
			return err
		}

		execOptions := containerExecOptions{
			noTTY: cmd.Bool(noTTYFlagName),
		}

		slog.Info("running command", "run", selectedRun)

		return runCommandWorkflow(ctx, selectedRun, startupOptions, execOptions, deps)
	}
}

func resolveWorkspaceStartupOptions(cmd *cli.Command) (workspaceStartupOptions, error) {
	options := defaultWorkspaceStartupOptions()
	if cmd.IsSet(startupTimeoutFlagName) {
		options.timeout = cmd.Duration(startupTimeoutFlagName)
	}

	if cmd.IsSet(statusIntervalFlagName) {
		options.statusInterval = cmd.Duration(statusIntervalFlagName)
	}

	err := validateWorkspaceStartupOptions(options)
	if err != nil {
		return workspaceStartupOptions{}, err
	}

	return options, nil
}

func defaultWorkspaceStartupOptions() workspaceStartupOptions {
	return workspaceStartupOptions{
		timeout:        defaultWorkspaceStartupTimeout,
		statusInterval: defaultWorkspaceStatusInterval,
	}
}

func validateWorkspaceStartupOptions(options workspaceStartupOptions) error {
	if options.timeout <= 0 {
		return fmt.Errorf("%s must be greater than zero", startupTimeoutFlagName)
	}

	if options.statusInterval <= 0 {
		return fmt.Errorf("%s must be greater than zero", statusIntervalFlagName)
	}

	return nil
}

func resolveProjectNameWith(getwd func() (string, error)) (string, error) {
	workingDirectory, err := getwd()
	if err != nil {
		return "", fmt.Errorf("read working directory: %w", err)
	}

	return projectNameFromPath(workingDirectory)
}

func projectNameFromPath(path string) (string, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return "", fmt.Errorf("invalid project path %q", path)
	}

	cleanPath := filepath.Clean(trimmedPath)
	if cleanPath == "." || cleanPath == string(filepath.Separator) {
		return "", fmt.Errorf("invalid project path %q", path)
	}

	projectName := filepath.Base(cleanPath)
	if projectName == "" || projectName == "." || projectName == string(filepath.Separator) {
		return "", fmt.Errorf("invalid project path %q", path)
	}

	return projectName, nil
}

func resolveWorkspacePath() (string, error) {
	return resolveWorkspacePathWith(
		os.Getwd,
		filepath.Abs,
		filepath.EvalSymlinks,
		os.Stat,
	)
}

func resolveWorkspacePathWith(
	getwd func() (string, error),
	abs func(string) (string, error),
	evalSymlinks func(string) (string, error),
	stat func(string) (os.FileInfo, error),
) (string, error) {
	workingDirectory, err := getwd()
	if err != nil {
		return "", fmt.Errorf("read working directory: %w", err)
	}

	workspacePath, err := canonicalizeWorkspacePath(workingDirectory, abs, evalSymlinks)
	if err != nil {
		return "", err
	}

	workspaceRoot, err := findWorkspaceRoot(workspacePath, stat)
	if err != nil {
		return "", err
	}

	return workspaceRoot, nil
}

func canonicalizeWorkspacePath(
	workspacePath string,
	abs func(string) (string, error),
	evalSymlinks func(string) (string, error),
) (string, error) {
	trimmedPath := strings.TrimSpace(workspacePath)
	if trimmedPath == "" {
		return "", errors.New("workspace path cannot be empty")
	}

	absolutePath, err := abs(trimmedPath)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}

	resolvedPath, err := evalSymlinks(absolutePath)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}

	resolvedPath = filepath.Clean(resolvedPath)
	if strings.TrimSpace(resolvedPath) == "" {
		return "", errors.New("workspace path cannot be empty")
	}

	return resolvedPath, nil
}

func findWorkspaceRoot(startPath string, stat func(string) (os.FileInfo, error)) (string, error) {
	searchPath := strings.TrimSpace(startPath)
	if searchPath == "" {
		return "", errors.New("workspace path cannot be empty")
	}

	searchPath = filepath.Clean(searchPath)
	for {
		devContainerPath := devContainerTemplatePath(searchPath)
		info, err := stat(devContainerPath)
		if err == nil {
			if info.IsDir() {
				return "", fmt.Errorf("devcontainer template path %q is a directory", devContainerPath)
			}
			return searchPath, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("stat devcontainer template: %w", err)
		}

		parentPath := filepath.Dir(searchPath)
		if parentPath == searchPath {
			break
		}
		searchPath = parentPath
	}

	return "", fmt.Errorf("devcontainer.json not found from %q", startPath)
}

func devContainerTemplatePath(workspaceRoot string) string {
	return filepath.Join(
		workspaceRoot,
		devContainerDirectoryName,
		devContainerTemplateFileName,
	)
}

func verifyContainerRunning(ctx context.Context, containerName string) (bool, error) {
	return verifyContainerRunningWith(ctx, containerName, runWithOutputImplementation, resolveWorkspacePath)
}

func verifyContainerRunningWith(
	ctx context.Context,
	workspaceRef string,
	runStatus runWithOutput,
	resolveWorkspacePath func() (string, error),
) (bool, error) {
	references, err := workspaceStatusReferences(workspaceRef, resolveWorkspacePath)
	if err != nil {
		return false, err
	}

	for _, reference := range references {
		running, err := workspaceRunningForReference(ctx, reference, runStatus)
		if err == nil {
			return running, nil
		}

		if errors.Is(err, errWorkspaceNotFound) {
			continue
		}

		return false, err
	}

	return false, nil
}

func workspaceStatusReferences(
	workspaceRef string,
	resolveWorkspacePath func() (string, error),
) ([]string, error) {
	reference := strings.TrimSpace(workspaceRef)
	references := []string{}
	if reference != "" {
		references = append(references, reference)
	}

	workspacePath, err := resolveWorkspacePath()
	if err != nil {
		if len(references) > 0 {
			return references, nil
		}

		return nil, err
	}

	if len(references) == 0 || references[0] != workspacePath {
		references = append(references, workspacePath)
	}

	return references, nil
}

func workspaceRunningForReference(ctx context.Context, workspaceRef string, runStatus runWithOutput) (bool, error) {
	status, err := readWorkspaceStatusWith(ctx, workspaceRef, runStatus, devPodStatusCommandTimeout)
	if err != nil {
		return false, err
	}

	return strings.EqualFold(status.State, "running"), nil
}

func readWorkspaceStatusWith(
	ctx context.Context,
	workspaceRef string,
	runStatus runWithOutput,
	statusTimeout time.Duration,
) (devPodWorkspaceStatus, error) {
	workspace := strings.TrimSpace(workspaceRef)
	if workspace == "" {
		return devPodWorkspaceStatus{}, errors.New("workspace reference cannot be empty")
	}

	statusCtx := ctx
	var cancel context.CancelFunc
	if statusTimeout > 0 {
		statusCtx, cancel = context.WithTimeout(ctx, statusTimeout)
		defer cancel()
	}

	output, runErr := runStatus(statusCtx, "devpod", devPodStatusAction, "--output", "json", workspace)
	if ctxErr := statusCommandContextError(ctx, runErr, statusTimeout); ctxErr != nil {
		return devPodWorkspaceStatus{}, ctxErr
	}

	return parseWorkspaceStatus(output, runErr)
}

// statusCommandContextError checks whether runErr is a context cancellation or
// deadline error and returns the appropriate sentinel. It returns nil when
// runErr is not context-related so the caller can continue with normal parsing.
func statusCommandContextError(parent context.Context, runErr error, timeout time.Duration) error {
	if runErr == nil {
		return nil
	}

	if !errors.Is(runErr, context.Canceled) && !errors.Is(runErr, context.DeadlineExceeded) {
		return nil
	}

	if parent.Err() != nil {
		return parent.Err()
	}

	return fmt.Errorf("%w: exceeded %v timeout", errDevPodStatusCommandTimeout, timeout)
}

func parseWorkspaceStatus(output commandOutput, runErr error) (devPodWorkspaceStatus, error) {
	statusOutput := workspaceStatusOutput(output)
	if statusOutput == "" {
		if runErr != nil {
			return devPodWorkspaceStatus{}, runStatusError(runErr, output)
		}

		return devPodWorkspaceStatus{}, fmt.Errorf("read workspace status: empty output%s", debugOutputSuffix(output))
	}

	status := devPodWorkspaceStatus{}
	parseErr := json.Unmarshal([]byte(statusOutput), &status)
	if parseErr != nil {
		if runErr != nil {
			return devPodWorkspaceStatus{}, runStatusError(runErr, output)
		}

		return devPodWorkspaceStatus{}, fmt.Errorf("parse workspace status: %w%s", parseErr, debugOutputSuffix(output))
	}

	if runErr != nil && (isWorkspaceNotFoundError(output.stderr) || isWorkspaceNotFoundError(output.stdout)) {
		return devPodWorkspaceStatus{}, errWorkspaceNotFound
	}

	if strings.TrimSpace(status.ID) == "" {
		return devPodWorkspaceStatus{}, fmt.Errorf("workspace status is missing id%s", debugOutputSuffix(output))
	}

	if strings.TrimSpace(status.State) == "" {
		return devPodWorkspaceStatus{}, fmt.Errorf("workspace status is missing state%s", debugOutputSuffix(output))
	}

	return status, nil
}

func runStatusError(runErr error, output commandOutput) error {
	if runErr == nil {
		return nil
	}

	if isWorkspaceNotFoundError(output.stderr) || isWorkspaceNotFoundError(output.stdout) {
		return errWorkspaceNotFound
	}

	return fmt.Errorf("read workspace status: %w%s", runErr, debugOutputSuffix(output))
}

func workspaceStatusOutput(output commandOutput) string {
	candidates := []string{
		strings.TrimSpace(output.stdout),
		strings.TrimSpace(output.stderr),
	}

	fallbackCandidate := ""

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}

		if fallbackCandidate == "" {
			fallbackCandidate = candidate
		}

		if json.Valid([]byte(candidate)) {
			return candidate
		}

		extracted := extractJSONObject(candidate)
		if extracted != "" {
			return extracted
		}
	}

	return fallbackCandidate
}

func extractJSONObject(raw string) string {
	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end < start {
		return ""
	}

	candidate := strings.TrimSpace(raw[start : end+1])
	if !json.Valid([]byte(candidate)) {
		return ""
	}

	return candidate
}

func isWorkspaceNotFoundError(output string) bool {
	lowerCaseOutput := strings.ToLower(output)

	return strings.Contains(lowerCaseOutput, "doesn't exist") ||
		strings.Contains(lowerCaseOutput, "does not exist") ||
		strings.Contains(lowerCaseOutput, "not found")
}

func debugOutputSuffix(output commandOutput) string {
	if !isDebugEnabled() {
		return ""
	}

	if output.stdout == "" && output.stderr == "" {
		return ""
	}

	return fmt.Sprintf(" (stdout=%q stderr=%q)", output.stdout, output.stderr)
}

func isDebugEnabled() bool {
	value, ok := os.LookupEnv(debugEnvVar)
	if !ok {
		return false
	}

	trimmedValue := strings.TrimSpace(strings.ToLower(value))
	if trimmedValue == "" || trimmedValue == "0" || trimmedValue == "false" || trimmedValue == "no" {
		return false
	}

	return true
}

func runCommandWorkflow(
	ctx context.Context,
	selectedRun string,
	startupOptions workspaceStartupOptions,
	execOptions containerExecOptions,
	deps runActionDependencies,
) error {
	err := validateWorkspaceStartupOptions(startupOptions)
	if err != nil {
		return err
	}

	err = deps.verifyDevContainerTemplate()
	if err != nil {
		slog.Error("error, while looking for devcontainer.json", "error", err.Error())
		return err
	}

	err = deps.verifyInstallation("docker")
	if err != nil {
		slog.Error("error, while checking for docker installation", "error", err.Error())
		return err
	}

	err = deps.verifyInstallation("devpod")
	if err != nil {
		slog.Error("error, while checking for docker installation", "error", err.Error())
		return err
	}

	workspacePath, err := deps.resolveWorkspacePath()
	if err != nil {
		slog.Error("error, unable to resolve workspace path", "error", err.Error())
		return err
	}

	err = ensureWorkspaceRunning(
		ctx,
		workspacePath,
		deps.verifyContainerRunning,
		deps.acquireWorkspaceLock,
		deps.startDevPodContainer,
		time.Sleep,
		startupOptions,
	)
	if err != nil {
		slog.Error("error, unable to ensure workspace is running", "workspacePath", workspacePath, "error", err.Error())
		return err
	}

	err = deps.executeRunInContainer(ctx, selectedRun, workspacePath, execOptions)
	if err != nil {
		slog.Error("error, unable to execute command in container", "run", selectedRun, "error", err.Error())
		return err
	}

	return nil
}

func ensureWorkspaceRunning(
	ctx context.Context,
	workspacePath string,
	verifyContainerRunning func(context.Context, string) (bool, error),
	acquireWorkspaceLock func(string) (releaseWorkspaceLock, error),
	startDevPodContainer func(context.Context, string, workspaceStartupOptions) error,
	sleep sleep,
	startupOptions workspaceStartupOptions,
) (retErr error) {
	workspace := strings.TrimSpace(workspacePath)
	if workspace == "" {
		return errors.New("workspace path cannot be empty")
	}

	err := validateWorkspaceStartupOptions(startupOptions)
	if err != nil {
		return err
	}

	if sleep == nil {
		sleep = time.Sleep
	}

	slog.Info("checking if workspace container is running")

	isContainerRunning, err := verifyContainerRunning(ctx, workspace)
	if err != nil {
		return err
	}

	if isContainerRunning {
		slog.Info("workspace container is already running")
		return nil
	}

	slog.Info("workspace container is not running, preparing to start")

	releaseLock, err := acquireWorkspaceLock(workspace)
	if err != nil {
		if errors.Is(err, errWorkspaceStartupInProgress) {
			slog.Info("another process is starting the workspace, waiting")
			return waitForWorkspaceRunningAfterLockContention(
				ctx,
				workspace,
				verifyContainerRunning,
				sleep,
				startupOptions,
			)
		}

		return err
	}

	if releaseLock == nil {
		return errors.New("workspace lock release function cannot be nil")
	}

	defer func() {
		releaseErr := releaseLock()
		if releaseErr == nil {
			return
		}

		wrappedErr := fmt.Errorf("release workspace startup lock: %w", releaseErr)
		if retErr == nil {
			retErr = wrappedErr
			return
		}

		retErr = errors.Join(retErr, wrappedErr)
	}()

	isContainerRunning, err = verifyContainerRunning(ctx, workspace)
	if err != nil {
		return err
	}

	if isContainerRunning {
		slog.Info("workspace container became running while waiting for lock")
		return nil
	}

	slog.Info("starting workspace container")

	return startDevPodContainer(ctx, workspace, startupOptions)
}

func waitForWorkspaceRunningAfterLockContention(
	ctx context.Context,
	workspacePath string,
	verifyContainerRunning func(context.Context, string) (bool, error),
	sleep sleep,
	startupOptions workspaceStartupOptions,
) error {
	workspace := strings.TrimSpace(workspacePath)
	if workspace == "" {
		return errors.New("workspace path cannot be empty")
	}

	err := validateWorkspaceStartupOptions(startupOptions)
	if err != nil {
		return err
	}

	if sleep == nil {
		sleep = time.Sleep
	}

	deadline := time.Now().Add(startupOptions.timeout)
	attempt := 1
	var lastErr error

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		slog.Info("waiting for workspace to become available", "attempt", attempt)

		isContainerRunning, err := verifyContainerRunning(ctx, workspace)
		if err == nil && isContainerRunning {
			slog.Info("workspace became available")
			return nil
		}

		if err != nil {
			lastErr = err
		} else {
			lastErr = fmt.Errorf("workspace %q is still starting", workspace)
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			return fmt.Errorf(
				"%w: workspace %q did not become running within %s while another startup is in progress: %w",
				errWorkspaceStartupTimeout,
				workspace,
				startupOptions.timeout,
				lastErr,
			)
		}

		nextDelay := workspaceStartupBackoffDuration(attempt, startupOptions.statusInterval)
		if nextDelay > remaining {
			nextDelay = remaining
		}

		err = waitForNextAttempt(ctx, sleep, nextDelay)
		if err != nil {
			return err
		}

		attempt++
	}
}

func workspaceStartupBackoffDuration(attempt int, interval time.Duration) time.Duration {
	if attempt <= 1 {
		return interval
	}

	delay := interval
	for i := 1; i < attempt; i++ {
		if delay >= maxWorkspaceStartupBackoffInterval {
			return maxWorkspaceStartupBackoffInterval
		}

		if delay > maxWorkspaceStartupBackoffInterval/2 {
			return maxWorkspaceStartupBackoffInterval
		}

		delay *= 2
	}

	if delay > maxWorkspaceStartupBackoffInterval {
		return maxWorkspaceStartupBackoffInterval
	}

	return delay
}

func acquireWorkspaceStartupLock(workspacePath string) (releaseWorkspaceLock, error) {
	workspace := strings.TrimSpace(workspacePath)
	if workspace == "" {
		return nil, errors.New("workspace path cannot be empty")
	}

	canonicalWorkspace, err := canonicalizeWorkspacePath(
		workspace,
		filepath.Abs,
		filepath.EvalSymlinks,
	)
	if err != nil {
		return nil, err
	}
	workspace = canonicalWorkspace

	lockPath := workspaceStartupLockPath(workspace)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open workspace startup lock file: %w", err)
	}

	err = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		closeErr := lockFile.Close()
		if closeErr != nil {
			err = errors.Join(err, closeErr)
		}

		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, fmt.Errorf("%w for %q", errWorkspaceStartupInProgress, workspace)
		}

		return nil, fmt.Errorf("acquire workspace startup lock: %w", err)
	}

	return func() error {
		unlockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
		closeErr := lockFile.Close()
		if unlockErr == nil && closeErr == nil {
			return nil
		}

		return fmt.Errorf("release workspace startup lock: %w", errors.Join(unlockErr, closeErr))
	}, nil
}

func workspaceStartupLockPath(workspacePath string) string {
	hash := sha256.Sum256([]byte(workspacePath))

	return filepath.Join(
		os.TempDir(),
		fmt.Sprintf("%s-%x.lock", workspaceStartupLockFileNamePrefix, hash[:]),
	)
}

func executeRunInContainer(
	ctx context.Context,
	selectedRun string,
	workspacePath string,
	options containerExecOptions,
) error {
	return executeRunInContainerWithWorkspaceDependencies(
		ctx,
		selectedRun,
		workspacePath,
		runImplementation,
		runWithOutputImplementation,
		runInteractiveImplementation,
		os.ReadFile,
		time.Sleep,
		options,
		isTerminalSession,
		os.LookupEnv,
	)
}

func executeRunInContainerWith(
	ctx context.Context,
	selectedRun string,
	runInteractive runInteractiveCmd,
) error {
	if strings.TrimSpace(selectedRun) == "" {
		return errors.New("run value cannot be empty")
	}

	workspacePath, err := resolveWorkspacePath()
	if err != nil {
		return err
	}

	return executeRunInContainerWithWorkspaceDependencies(
		ctx,
		selectedRun,
		workspacePath,
		runImplementation,
		runWithOutputImplementation,
		runInteractive,
		os.ReadFile,
		time.Sleep,
		containerExecOptions{},
		isTerminalSession,
		os.LookupEnv,
	)
}

func executeRunInContainerWithWorkspaceDependencies(
	ctx context.Context,
	selectedRun string,
	workspacePath string,
	run run,
	runStatus runWithOutput,
	runInteractive runInteractiveCmd,
	readFile readFile,
	sleep sleep,
	options containerExecOptions,
	isTTY func() bool,
	lookupEnv func(string) (string, bool),
) error {
	command := strings.TrimSpace(selectedRun)
	if command == "" {
		return errors.New("run value cannot be empty")
	}

	workspace := strings.TrimSpace(workspacePath)
	if workspace == "" {
		return errors.New("workspace path cannot be empty")
	}

	workspaceStatus, err := readWorkspaceStatusWith(ctx, workspace, runStatus, devPodStatusCommandTimeout)
	if err != nil {
		if errors.Is(err, errWorkspaceNotFound) {
			return errors.New("workspace is not created yet; run the command again to create it")
		}

		return err
	}

	if !strings.EqualFold(workspaceStatus.State, "running") {
		return fmt.Errorf("workspace %q is %q", workspaceStatus.ID, workspaceStatus.State)
	}

	provider := strings.TrimSpace(workspaceStatus.Provider)
	if shouldUseDevPodSSH(ctx, provider, run) {
		slog.Info("executing command via devpod ssh", "command", command)

		err = executeRunWithDevPodSSH(ctx, workspaceStatus, command, runInteractive)
		if err != nil {
			return err
		}

		return nil
	}

	if !strings.EqualFold(provider, "docker") {
		return fmt.Errorf(
			"workspace provider %q does not support docker exec and devpod ssh is unavailable",
			workspaceStatus.Provider,
		)
	}

	slog.Info("resolving container id")

	containerID, err := resolveWorkspaceContainerIDWithRetry(
		ctx,
		workspaceStatus,
		readFile,
		run,
		sleep,
		containerIDMaxAttempts,
		containerIDAttemptInterval,
	)
	if err != nil {
		return err
	}

	runtimeConfig := resolveContainerExecRuntimeConfig(workspace, readFile, lookupEnv)
	execShell := resolveContainerShell(ctx, containerID, runtimeConfig, run)
	shellFlag := shellCommandFlag(execShell)
	allocateTTY := !options.noTTY && isTTY != nil && isTTY()
	execArgs := dockerExecArgs(containerID, runtimeConfig.user, execShell, shellFlag, command, allocateTTY)

	slog.Info("executing command in container", "command", command, "container", containerID)

	err = runInteractive(ctx, "docker", execArgs...)
	if err != nil {
		return fmt.Errorf("execute command in container: %w", err)
	}

	return nil
}

func shouldUseDevPodSSH(ctx context.Context, provider string, run run) bool {
	if strings.EqualFold(strings.TrimSpace(provider), "docker") {
		return false
	}

	return isDevPodSSHAvailable(ctx, run)
}

func isDevPodSSHAvailable(ctx context.Context, run run) bool {
	if run == nil {
		return false
	}

	_, err := run(ctx, "devpod", devPodSSHAction, "--help")

	return err == nil
}

func executeRunWithDevPodSSH(
	ctx context.Context,
	status devPodWorkspaceStatus,
	command string,
	runInteractive runInteractiveCmd,
) error {
	if runInteractive == nil {
		return errors.New("interactive command runner cannot be nil")
	}

	workspaceID := strings.TrimSpace(status.ID)
	if workspaceID == "" {
		return errors.New("workspace id cannot be empty")
	}

	args := []string{
		devPodSSHAction,
		workspaceID,
		"--",
		defaultContainerShell,
		shellCommandFlag(defaultContainerShell),
		command,
	}

	err := runInteractive(ctx, "devpod", args...)
	if err != nil {
		return fmt.Errorf("execute command via devpod ssh: %w", err)
	}

	return nil
}

func resolveContainerExecRuntimeConfig(
	workspacePath string,
	readFile readFile,
	lookupEnv func(string) (string, bool),
) containerExecRuntimeConfig {
	config := containerExecRuntimeConfig{
		user:  defaultContainerUser,
		shell: defaultContainerShell,
	}

	devContainerRaw, err := readFile(devContainerTemplatePath(workspacePath))
	if err == nil {
		parsedConfig, parseErr := parseDevContainerRuntimeConfig(devContainerRaw)
		if parseErr == nil {
			config = mergeContainerExecRuntimeConfig(config, parsedConfig)
		}
	}

	if value := envOverrideValue(lookupEnv, containerUserOverrideEnvVar); value != "" {
		config.user = value
	}

	if value := envOverrideValue(lookupEnv, containerShellOverrideEnvVar); value != "" {
		config.shell = value
	}

	return config
}

func mergeContainerExecRuntimeConfig(
	base containerExecRuntimeConfig,
	override containerExecRuntimeConfig,
) containerExecRuntimeConfig {
	if override.user != "" {
		base.user = override.user
	}

	if override.shell != "" {
		base.shell = override.shell
	}

	return base
}

func envOverrideValue(lookupEnv func(string) (string, bool), key string) string {
	if lookupEnv == nil {
		return ""
	}

	value, ok := lookupEnv(key)
	if !ok {
		return ""
	}

	return strings.TrimSpace(value)
}

func parseDevContainerRuntimeConfig(raw []byte) (containerExecRuntimeConfig, error) {
	config := devContainerTemplate{}
	err := json.Unmarshal(raw, &config)
	if err != nil {
		return containerExecRuntimeConfig{}, err
	}

	user := strings.TrimSpace(config.RemoteUser)
	if user == "" {
		user = strings.TrimSpace(config.ContainerUser)
	}

	shell := ""
	if config.RemoteEnv != nil {
		shellValue, ok := config.RemoteEnv["SHELL"]
		if ok {
			shellString, shellOK := shellValue.(string)
			if shellOK {
				shell = strings.TrimSpace(shellString)
			}
		}
	}

	return containerExecRuntimeConfig{user: user, shell: shell}, nil
}

func resolveContainerShell(
	ctx context.Context,
	containerID string,
	config containerExecRuntimeConfig,
	run run,
) string {
	shell := strings.TrimSpace(config.shell)
	if shell == "" {
		shell = defaultContainerShell
	}

	if !isZshShell(shell) {
		return shell
	}

	if shellAvailableInContainer(ctx, containerID, config.user, shell, run) {
		return shell
	}

	return fallbackContainerShell
}

func shellAvailableInContainer(
	ctx context.Context,
	containerID string,
	containerUser string,
	shell string,
	run run,
) bool {
	if run == nil {
		return false
	}

	args := []string{"exec"}
	user := strings.TrimSpace(containerUser)
	if user != "" {
		args = append(args, "--user", user)
	}

	args = append(args, containerID, shell, "--version")
	_, err := run(ctx, "docker", args...)

	return err == nil
}

func dockerExecArgs(
	containerID string,
	containerUser string,
	shell string,
	shellFlag string,
	command string,
	allocateTTY bool,
) []string {
	args := []string{"exec", "-i"}
	if allocateTTY {
		args = append(args, "-t")
	}

	user := strings.TrimSpace(containerUser)
	if user != "" {
		args = append(args, "--user", user)
	}

	args = append(args, containerID, shell, shellFlag, command)

	return args
}

func shellCommandFlag(shell string) string {
	if isZshShell(shell) {
		return "-ic"
	}

	return "-lc"
}

func isZshShell(shell string) bool {
	trimmedShell := strings.TrimSpace(shell)
	if trimmedShell == "" {
		return false
	}

	return strings.EqualFold(filepath.Base(trimmedShell), "zsh")
}

func isTerminalSession() bool {
	return isTerminalFile(os.Stdin) && isTerminalFile(os.Stdout)
}

func isTerminalFile(file *os.File) bool {
	if file == nil {
		return false
	}

	info, err := file.Stat()
	if err != nil {
		return false
	}

	return info.Mode()&os.ModeCharDevice != 0
}

func resolveWorkspaceContainerIDWith(
	ctx context.Context,
	status devPodWorkspaceStatus,
	readFile readFile,
	run run,
) (string, error) {
	return resolveWorkspaceContainerIDWithRetry(
		ctx,
		status,
		readFile,
		run,
		time.Sleep,
		containerIDMaxAttempts,
		containerIDAttemptInterval,
	)
}

func resolveWorkspaceContainerIDWithRetry(
	ctx context.Context,
	status devPodWorkspaceStatus,
	readFile readFile,
	run run,
	sleep sleep,
	maxAttempts int,
	interval time.Duration,
) (string, error) {
	err := validateHealthCheckConfig(maxAttempts, interval)
	if err != nil {
		return "", err
	}

	var primaryErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		containerID, err := resolveWorkspaceContainerIDFromResult(status, readFile)
		if err == nil {
			return containerID, nil
		}

		primaryErr = err

		if attempt < maxAttempts {
			err = waitForNextAttempt(ctx, sleep, interval)
			if err != nil {
				return "", err
			}
		}
	}

	containerID, fallbackErr := resolveWorkspaceContainerIDByDockerFiltersWith(ctx, status.ID, run)
	if fallbackErr == nil {
		return containerID, nil
	}

	containerID, imagePrefixErr := resolveWorkspaceContainerIDByImagePrefixWith(ctx, status.ID, run)
	if imagePrefixErr == nil {
		return containerID, nil
	}

	return "", fmt.Errorf("resolve container id: %w", errors.Join(primaryErr, fallbackErr, imagePrefixErr))
}

func resolveWorkspaceContainerIDFromResult(status devPodWorkspaceStatus, readFile readFile) (string, error) {
	resultPath, err := resolveWorkspaceResultPath(status)
	if err != nil {
		return "", err
	}

	workspaceResultRaw, err := readFile(resultPath)
	if err != nil {
		return "", fmt.Errorf("read workspace result: %w", err)
	}

	containerID, err := parseWorkspaceContainerID(workspaceResultRaw)
	if err != nil {
		return "", fmt.Errorf("parse workspace result: %w", err)
	}

	return containerID, nil
}

func resolveWorkspaceResultPath(status devPodWorkspaceStatus) (string, error) {
	workspaceID, err := sanitizeWorkspaceResultPathSegment("workspace id", status.ID)
	if err != nil {
		return "", err
	}

	workspaceContext := strings.TrimSpace(status.Context)
	if workspaceContext == "" {
		workspaceContext = defaultWorkspaceContextName
	}

	workspaceContext, err = sanitizeWorkspaceResultPathSegment("workspace context", workspaceContext)
	if err != nil {
		return "", err
	}

	devPodHome, err := resolveDevPodHome()
	if err != nil {
		return "", err
	}

	resultPath := filepath.Join(
		devPodHome,
		"contexts",
		workspaceContext,
		"workspaces",
		workspaceID,
		workspaceResultFileName,
	)

	contextsRoot := filepath.Join(devPodHome, "contexts")
	relPath, err := filepath.Rel(contextsRoot, resultPath)
	if err != nil {
		return "", fmt.Errorf("resolve workspace result path: %w", err)
	}

	if relPath == ".." || strings.HasPrefix(relPath, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("workspace result path %q escapes DEVPOD_HOME", resultPath)
	}

	return resultPath, nil
}

func resolveDevPodHome() (string, error) {
	return resolveDevPodHomeWith(os.LookupEnv, os.UserHomeDir, filepath.Abs)
}

func resolveDevPodHomeWith(
	lookupEnv func(string) (string, bool),
	userHomeDir func() (string, error),
	abs func(string) (string, error),
) (string, error) {
	configuredHome := ""
	if lookupEnv != nil {
		value, ok := lookupEnv("DEVPOD_HOME")
		if ok {
			configuredHome = strings.TrimSpace(value)
		}
	}

	homeDirectory := ""
	resolveUserHome := func() (string, error) {
		if homeDirectory != "" {
			return homeDirectory, nil
		}

		home, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve user home directory: %w", err)
		}

		homeDirectory = home

		return homeDirectory, nil
	}

	if strings.HasPrefix(configuredHome, "~"+string(filepath.Separator)) || configuredHome == "~" {
		home, err := resolveUserHome()
		if err != nil {
			return "", err
		}

		relativeHome := strings.TrimPrefix(configuredHome, "~")
		relativeHome = strings.TrimLeft(relativeHome, "/\\")
		configuredHome = filepath.Join(home, relativeHome)
	}

	if configuredHome != "" {
		resolvedHome, err := abs(configuredHome)
		if err != nil {
			return "", fmt.Errorf("resolve DEVPOD_HOME: %w", err)
		}

		resolvedHome = filepath.Clean(strings.TrimSpace(resolvedHome))
		if resolvedHome == "" {
			return "", errors.New("resolve DEVPOD_HOME: resolved path is empty")
		}

		return resolvedHome, nil
	}

	home, err := resolveUserHome()
	if err != nil {
		return "", err
	}

	return filepath.Join(home, ".devpod"), nil
}

func sanitizeWorkspaceResultPathSegment(segmentName, value string) (string, error) {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return "", fmt.Errorf("%s cannot be empty", segmentName)
	}

	if trimmedValue == "." || trimmedValue == ".." {
		return "", fmt.Errorf("%s %q is invalid", segmentName, value)
	}

	if strings.Contains(trimmedValue, "/") || strings.Contains(trimmedValue, "\\") {
		return "", fmt.Errorf("%s %q cannot contain path separators", segmentName, value)
	}

	return trimmedValue, nil
}

func parseWorkspaceContainerID(raw []byte) (string, error) {
	result := devPodWorkspaceResult{}
	err := json.Unmarshal(raw, &result)
	if err != nil {
		return "", err
	}

	if result.ContainerDetails == nil {
		return "", errors.New("workspace result is missing container details")
	}

	containerID := strings.TrimSpace(result.ContainerDetails.ID)
	if containerID == "" {
		return "", errors.New("workspace result is missing container id")
	}

	return containerID, nil
}

func resolveWorkspaceContainerIDByDockerFiltersWith(
	ctx context.Context,
	workspaceID string,
	run run,
) (string, error) {
	targetWorkspaceID := strings.TrimSpace(workspaceID)
	if targetWorkspaceID == "" {
		return "", errors.New("workspace id cannot be empty")
	}

	var lastErr error
	for _, labelKey := range dockerWorkspaceLabelKeys {
		containerID, err := resolveWorkspaceContainerIDByDockerLabelWith(ctx, labelKey, targetWorkspaceID, run)
		if err == nil {
			return containerID, nil
		}

		if errors.Is(err, errWorkspaceContainerNotFound) {
			lastErr = err
			continue
		}

		return "", err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("%w for workspace %q", errWorkspaceContainerNotFound, targetWorkspaceID)
	}

	return "", lastErr
}

func resolveWorkspaceContainerIDByDockerLabelWith(
	ctx context.Context,
	labelKey string,
	workspaceID string,
	run run,
) (string, error) {
	targetLabelKey := strings.TrimSpace(labelKey)
	if targetLabelKey == "" {
		return "", errors.New("label key cannot be empty")
	}

	output, err := run(
		ctx,
		"docker",
		"ps",
		"--filter",
		fmt.Sprintf("label=%s=%s", targetLabelKey, workspaceID),
		"--format",
		"{{.ID}}",
	)
	if err != nil {
		return "", fmt.Errorf("list running containers by label %q: %w", targetLabelKey, err)
	}

	containerIDs := parseDockerPSIDs(output)
	if len(containerIDs) == 0 {
		return "", fmt.Errorf(
			"%w for workspace %q using label %q",
			errWorkspaceContainerNotFound,
			workspaceID,
			targetLabelKey,
		)
	}

	if len(containerIDs) > 1 {
		return "", fmt.Errorf(
			"%w for workspace %q using label %q",
			errMultipleWorkspaceContainers,
			workspaceID,
			targetLabelKey,
		)
	}

	return containerIDs[0], nil
}

func parseDockerPSIDs(output string) []string {
	lines := strings.Split(output, "\n")
	containerIDs := make([]string, 0, len(lines))
	for _, line := range lines {
		containerID := strings.TrimSpace(line)
		if containerID == "" {
			continue
		}

		containerIDs = append(containerIDs, containerID)
	}

	return containerIDs
}

func resolveWorkspaceContainerIDByImagePrefixWith(ctx context.Context, workspaceID string, run run) (string, error) {
	targetWorkspaceID := strings.TrimSpace(workspaceID)
	if targetWorkspaceID == "" {
		return "", errors.New("workspace id cannot be empty")
	}

	output, err := run(ctx, "docker", "ps", "--format", "{{.ID}}\t{{.Image}}")
	if err != nil {
		return "", fmt.Errorf("list running containers: %w", err)
	}

	imagePrefix := fmt.Sprintf("vsc-%s-", targetWorkspaceID)
	matches := []string{}
	for _, line := range strings.Split(output, "\n") {
		trimmedLine := strings.TrimSpace(line)
		if trimmedLine == "" {
			continue
		}

		parts := strings.SplitN(trimmedLine, "\t", 2)
		if len(parts) != 2 {
			continue
		}

		containerID := strings.TrimSpace(parts[0])
		image := strings.TrimSpace(parts[1])
		if containerID == "" || image == "" {
			continue
		}

		if strings.HasPrefix(image, imagePrefix) {
			matches = append(matches, containerID)
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("%w for workspace %q", errWorkspaceContainerNotFound, targetWorkspaceID)
	}

	if len(matches) > 1 {
		return "", fmt.Errorf("%w for workspace %q", errMultipleWorkspaceContainers, targetWorkspaceID)
	}

	return matches[0], nil
}

func hasRunSelection(flagValue string, flagSet bool, args []string) bool {
	if flagSet || strings.TrimSpace(flagValue) != "" {
		return true
	}

	for _, arg := range args {
		key, _, hasAssignment := strings.Cut(arg, "=")
		if hasAssignment && strings.TrimSpace(key) == runFlagName {
			return true
		}
	}

	return false
}

func resolveRunSelection(flagValue string, flagSet bool, args []string) (string, error) {
	runValue := strings.TrimSpace(flagValue)
	if runValue != "" {
		return runValue, nil
	}

	if flagSet {
		return "", errors.New("run value cannot be empty")
	}

	for _, arg := range args {
		key, value, hasAssignment := strings.Cut(arg, "=")
		if !hasAssignment || strings.TrimSpace(key) != runFlagName {
			continue
		}

		runValue = strings.TrimSpace(value)
		if runValue == "" {
			return "", errors.New("run value cannot be empty")
		}

		return runValue, nil
	}

	return "", errors.New("run is required (use --run=<command>, -run=<command>, -r=<command>, or run=<command>)")
}

func verifyContainerHealthWith(
	ctx context.Context,
	workspacePath string,
	runStatus runWithOutput,
	sleep sleep,
	timeout time.Duration,
	interval time.Duration,
) error {
	workspace := strings.TrimSpace(workspacePath)
	if workspace == "" {
		return errors.New("workspace path cannot be empty")
	}

	err := validateWorkspaceHealthCheckConfig(timeout, interval)
	if err != nil {
		return err
	}

	maxAttempts := healthCheckAttempts(timeout, interval)

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		slog.Info("checking container health", "attempt", attempt, "maxAttempts", maxAttempts)

		status, err := readWorkspaceStatusWith(ctx, workspace, runStatus, devPodStatusCommandTimeout)
		if err == nil {
			state := strings.ToLower(strings.TrimSpace(status.State))
			switch {
			case isWorkspaceStateRunning(state):
				slog.Info("container is healthy")
				return nil
			case isWorkspaceStateTerminalFailure(state):
				return fmt.Errorf(
					"%w: workspace %q entered terminal state %q while starting",
					errContainerUnhealthy,
					status.ID,
					status.State,
				)
			default:
				lastErr = fmt.Errorf("workspace %q is %q", status.ID, status.State)
			}
		} else {
			lastErr = err
		}

		if attempt < maxAttempts {
			err := waitForNextAttempt(ctx, sleep, interval)
			if err != nil {
				return err
			}
		}
	}

	if lastErr == nil {
		lastErr = errors.New("workspace health check ended without status")
	}

	return fmt.Errorf(
		"%w: workspace %q did not become running within %s (status interval %s); increase --%s or check `devpod logs %s`: %w",
		errWorkspaceStartupTimeout,
		workspace,
		timeout,
		interval,
		startupTimeoutFlagName,
		workspace,
		lastErr,
	)
}

func validateWorkspaceHealthCheckConfig(timeout, interval time.Duration) error {
	if timeout <= 0 {
		return fmt.Errorf("%s must be greater than zero", startupTimeoutFlagName)
	}

	if interval <= 0 {
		return fmt.Errorf("%s must be greater than zero", statusIntervalFlagName)
	}

	return nil
}

func healthCheckAttempts(timeout, interval time.Duration) int {
	if timeout <= 0 || interval <= 0 {
		return 0
	}

	attempts := int(timeout / interval)
	if timeout%interval != 0 {
		attempts++
	}

	if attempts < 1 {
		attempts = 1
	}

	return attempts
}

func validateHealthCheckConfig(maxAttempts int, interval time.Duration) error {
	if maxAttempts <= 0 {
		return errors.New("max attempts must be greater than zero")
	}

	if interval < 0 {
		return errors.New("interval cannot be negative")
	}

	return nil
}

func waitForNextAttempt(ctx context.Context, sleep sleep, interval time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	sleep(interval)

	if err := ctx.Err(); err != nil {
		return err
	}

	return nil
}

func isWorkspaceStateRunning(state string) bool {
	return state == "running"
}

func isWorkspaceStateTerminalFailure(state string) bool {
	switch state {
	case "stopped", "failed", "error", "errored", "unhealthy", "exited":
		return true
	default:
		return false
	}
}

func startDevPodContainer(
	ctx context.Context,
	workspacePath string,
	startupOptions workspaceStartupOptions,
) error {
	return startDevPodContainerWith(
		ctx,
		workspacePath,
		runImplementation,
		runWithOutputImplementation,
		startupOptions,
	)
}

func startDevPodContainerWith(
	ctx context.Context,
	workspacePath string,
	run run,
	runStatus runWithOutput,
	startupOptions workspaceStartupOptions,
) error {
	workspace := strings.TrimSpace(workspacePath)
	if workspace == "" {
		return errors.New("workspace path cannot be empty")
	}

	err := validateWorkspaceStartupOptions(startupOptions)
	if err != nil {
		return err
	}

	slog.Info("running devpod up", "workspace", workspace)

	upCtx, upCancel := context.WithTimeout(ctx, startupOptions.timeout)
	defer upCancel()

	_, err = run(upCtx, "devpod", "up", workspace, "--open-ide=false", "--ide", "none")
	if err != nil {
		if upCtx.Err() != nil && ctx.Err() == nil {
			return fmt.Errorf("start container: devpod up timed out after %v", startupOptions.timeout)
		}

		return fmt.Errorf("start container: %w", err)
	}

	slog.Info("devpod up completed, verifying container health")

	return verifyContainerHealthWith(
		ctx,
		workspace,
		runStatus,
		time.Sleep,
		startupOptions.timeout,
		startupOptions.statusInterval,
	)
}

func verifyInstallation(application string) error {
	return verifyInstallationWithLookPath(application, exec.LookPath)
}

func verifyInstallationWithLookPath(application string, lookPath func(string) (string, error)) error {
	if application == "" {
		return errors.New("application name cannot be empty")
	}

	_, err := lookPath(application)
	if err != nil {
		return fmt.Errorf("%w: %q not found in PATH: %w", errDependencyMissing, application, err)
	}

	return nil
}

func verifyDevContainerTemplate() error {
	workspaceRoot, err := resolveWorkspacePath()
	if err != nil {
		return err
	}

	_, err = os.Stat(devContainerTemplatePath(workspaceRoot))
	if err != nil {
		return err
	}

	return nil
}

func runImplementation(ctx context.Context, name string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	out := strings.TrimSpace(string(output))
	if err != nil {
		if out == "" {
			return "", err
		}

		return "", fmt.Errorf("%w: %s", err, out)
	}

	return out, nil
}

func runWithOutputImplementation(ctx context.Context, name string, args ...string) (commandOutput, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	output := commandOutput{
		stdout: strings.TrimSpace(stdout.String()),
		stderr: strings.TrimSpace(stderr.String()),
	}
	if err != nil {
		return output, err
	}

	return output, nil
}

func runInteractiveImplementation(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if err != nil {
		return fmt.Errorf("run %s: %w", name, err)
	}

	return nil
}
