package agentutil

import (
	"fmt"
	"os/exec"
	"strings"
)

// RequireDir validates and normalizes provider working directory input.
func RequireDir(provider, dir string) (string, error) {
	normalizedProvider := strings.TrimSpace(provider)
	if normalizedProvider == "" {
		normalizedProvider = "agent"
	}

	normalizedDir := strings.TrimSpace(dir)
	if normalizedDir == "" {
		return "", fmt.Errorf("%s: Dir is required", normalizedProvider)
	}
	return normalizedDir, nil
}

// PreflightBinary checks that a provider binary is available in PATH.
func PreflightBinary(binary string) error {
	normalizedBinary := strings.TrimSpace(binary)
	if normalizedBinary == "" {
		return fmt.Errorf("binary name is required")
	}

	if _, err := exec.LookPath(normalizedBinary); err != nil {
		return fmt.Errorf("%s binary not found in PATH: %w", normalizedBinary, err)
	}
	return nil
}
