package commands

import (
	"errors"
	"strings"
)

const (
	exitCodeOK                   = 0
	exitCodeGeneralFailure       = 1
	exitCodeUsageError           = 2
	exitCodeWorkspaceUnavailable = 3
	exitCodeDependencyMissing    = 127
)

// ExitCodeForError maps command errors into process exit codes.
func ExitCodeForError(err error) int {
	if err == nil {
		return exitCodeOK
	}

	switch {
	case errors.Is(err, errDependencyMissing):
		return exitCodeDependencyMissing
	case errors.Is(err, errWorkspaceNotFound):
		return exitCodeWorkspaceUnavailable
	case errors.Is(err, errWorkspaceStartupInProgress):
		return exitCodeWorkspaceUnavailable
	case errors.Is(err, errWorkspaceStartupTimeout):
		return exitCodeWorkspaceUnavailable
	case errors.Is(err, errContainerUnhealthy):
		return exitCodeWorkspaceUnavailable
	case errors.Is(err, errWorkspaceContainerNotFound):
		return exitCodeWorkspaceUnavailable
	case errors.Is(err, errMultipleWorkspaceContainers):
		return exitCodeWorkspaceUnavailable
	case isRunUsageError(err):
		return exitCodeUsageError
	default:
		return exitCodeGeneralFailure
	}
}

func isRunUsageError(err error) bool {
	message := strings.TrimSpace(strings.ToLower(err.Error()))

	return strings.Contains(message, "run value cannot be empty") ||
		strings.Contains(message, "run is required")
}
