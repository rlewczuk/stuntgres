package engine

import (
	"stuntgres/internal/engine/typedef"
)

func (b *expressionBinder) collationName(id uint32) string {
	view := b.Host.CurrentCatalog()
	if b.Query.Auth != nil {
		view = b.Query.Auth.ReadCatalog()
	}
	return typedef.CollationName(view, id)
}
