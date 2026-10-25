package core

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func (e *Engine) IndexExpressionBinder(op *session.Operation, input sqltype.InputEnvironment, auth AuthorizationView, parent *catalog.Relation) *analyze.Binder {
	ref := e.Catalog.Domain.Ref(auth.Catalog.Epoch(), parent.Address())
	columns := make([]analyze.QueryColumn, len(parent.Columns))
	for i, c := range parent.Columns {
		columns[i] = analyze.QueryColumn{Descriptor: c, Slot: i, Leaf: 0, Physical: i}
	}
	namespace := analyze.QueryNamespace{Columns: columns, Qualifiers: []analyze.QueryQualifier{{Name: parent.Name(), Original: parent.Name(), Schema: auth.Catalog.Get(parent.Namespace()).Name(), Ref: ref, Columns: columns}}, Frame: parent.Columns}
	binder := NewExpressionBinder(op.Context(), e, input, op, nil)
	binder.Consumer.IndexConsumer, binder.Params.NoParameters, binder.Query.Auth = true, true, &auth
	binder.Query.Namespace = &namespace
	binder.Query.Sources = []exec.Leaf{{Ref: ref, Relation: parent}}
	return binder
}
