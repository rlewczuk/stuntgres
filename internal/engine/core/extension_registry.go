package core

import (
	"fmt"

	"stuntgres/internal/engine/extension"
)

// Registration is initialization-only: the registry closes when New seeds the
// catalog. extension.Register holds the checks.
func (e *Engine) registerExtension(name string, implementation extension.Installer) error {
	if e.Catalog.State != nil {
		return fmt.Errorf("stuntgres: extension registry is closed")
	}
	registry, err := extension.Register(e.Catalog.Extensions, name, implementation)
	if err != nil {
		return err
	}
	e.Catalog.Extensions = registry
	return nil
}
