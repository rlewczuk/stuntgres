package extension

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
)

// Register returns registry with implementation added under name, allocating
// it when nil. Registration is initialization-only and duplicate checked,
// like catalog provider registration; it does not execute scripts or load
// external packages. On error it returns nil and leaves registry unchanged.
func Register(registry map[string]catalog.ExtensionImplementation, name string, implementation Installer) (map[string]catalog.ExtensionImplementation, error) {
	if name == "" || implementation == nil || implementation.Control().Version == "" {
		return nil, fmt.Errorf("stuntgres: invalid extension registration")
	}
	if catalog.ExtensionNameDetail(name, false) != "" {
		return nil, fmt.Errorf("stuntgres: invalid extension registration name")
	}
	if err := ValidateActions(implementation); err != nil {
		return nil, err
	}
	if _, exists := registry[name]; exists {
		return nil, fmt.Errorf("stuntgres: duplicate extension registration %q", name)
	}
	if registry == nil {
		registry = make(map[string]catalog.ExtensionImplementation)
	}
	registry[name] = implementation
	return registry, nil
}
