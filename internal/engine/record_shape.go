package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// Anonymous record shapes are occurrence authorities, not catalog providers.
// Every concrete leaf still belongs to the current catalog generation.
func validExpressionUsage(view catalog.Read, usage sqltype.Usage) bool {
	return validExpressionUsageAt(view, nil, usage)
}

func validExpressionUsageAt(view catalog.Read, reader catalog.DefinitionReader, usage sqltype.Usage) bool {
	if !usage.Valid() || validateUsageCollationAt(view, reader, usage) != nil {
		return false
	}
	typ := usage.Type
	if typ.IsDeferredRecord() {
		return true
	}
	if sqltype.OccurrenceRecordArray(typ) {
		return validExpressionUsageAt(view, reader, typ.Element().Usage())
	}
	if typ.ID() == 2249 && typ.IsComposite() {
		for i := range typ.CompositeLen() {
			if !validExpressionUsageAt(view, reader, typ.CompositeMember(i).Usage) {
				return false
			}
		}
		return true
	}
	current, ok := catalog.DefinitionTypeAt(view, reader, typ.ID())
	return ok && current == typ
}
