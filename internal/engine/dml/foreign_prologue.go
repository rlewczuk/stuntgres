package dml

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
)

// ForeignCommandPrologue decides a foreign-table INSERT, UPDATE or DELETE
// before its source capture. It rejects ON CONFLICT and WHERE CURRENT OF, in
// that order, then returns stmt's modify operation and the invoker the
// foreign source runs as. A view write takes it from its last layer: that
// view's owner, or current for a security_invoker view; otherwise current.
func ForeignCommandPrologue(stmt parser.Statement, b *analyze.Binding, current authorization.PrincipalRef) (catalog.ForeignModifyOperation, authorization.PrincipalRef, error) {
	if b.Insert.Conflict != nil {
		return 0, authorization.PrincipalRef{}, sqlError("0A000", "ON CONFLICT on foreign tables is unsupported in embedded mode")
	}
	if b.CurrentOf != "" {
		return 0, authorization.PrincipalRef{}, sqlError("0A000", "positioned foreign modification is unsupported in embedded mode")
	}
	op := catalog.ForeignInsert
	switch stmt.(type) {
	case *parser.Update:
		op = catalog.ForeignUpdate
	case *parser.Delete:
		op = catalog.ForeignDelete
	}
	invoker := current
	if b.ViewWrite != nil {
		for _, layer := range view.ViewWriteOf(b.ViewWrite).Layers {
			invoker = layer.Relation.Owner
			if stored.ViewOf(layer.Relation).Invoker() {
				invoker = current
			}
		}
	}
	return op, invoker, nil
}
