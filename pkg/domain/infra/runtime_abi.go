//go:build !remote && (linux || freebsd)

package infra

import (
	"fmt"

	"go.podman.io/podman/v6/pkg/domain/entities"
)

// NewContainerEngine factory provides a libpod runtime for container-related operations
func NewContainerEngine(facts *entities.PodmanConfig) (entities.ContainerEngine, error) {
	switch facts.EngineMode {
	case entities.ABIMode:
		r, err := NewLibpodRuntime(facts.FlagSet, facts)
		return r, err
	}
	return nil, fmt.Errorf("runtime mode '%v' is not supported", facts.EngineMode)
}

// NewImageEngine factory provides a libpod runtime for image-related operations
func NewImageEngine(facts *entities.PodmanConfig) (entities.ImageEngine, error) {
	switch facts.EngineMode {
	case entities.ABIMode:
		r, err := NewLibpodImageRuntime(facts.FlagSet, facts)
		return r, err
	}
	return nil, fmt.Errorf("runtime mode '%v' is not supported", facts.EngineMode)
}
