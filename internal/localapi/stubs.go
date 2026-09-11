// easytidy: machine feature removed; localapi helpers reduced to no-ops.
package localapi

// ValidatePathForLocalAPI always succeeds without a machine.
func ValidatePathForLocalAPI(path string) error { return nil }

// IsWSLProvider is always false on non-machine builds.
func IsWSLProvider() bool { return false }

// IsHyperVProvider is always false on non-machine builds.
func IsHyperVProvider() bool { return false }

// CheckPathOnRunningMachine is a no-op without a machine.
func CheckPathOnRunningMachine(path string) error { return nil }

// CheckIfImageBuildPathsOnRunningMachine is a no-op without a machine.
func CheckIfImageBuildPathsOnRunningMachine(paths []string) error { return nil }
