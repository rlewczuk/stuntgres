package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func (e *Engine) remapCatalogExpressions(next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress) error {
	return stored.ReownExpressions(e.catalog.read(next), next, mapping, false)
}
