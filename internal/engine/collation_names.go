package engine

import (
	"stuntgres/internal/engine/typedef"
)

func (b *expressionBinder) collationName(id uint32) string {
	view := b.host.CurrentCatalog()
	if b.query.auth != nil {
		view = b.query.auth.ReadCatalog()
	}
	return typedef.CollationName(view, id)
}
