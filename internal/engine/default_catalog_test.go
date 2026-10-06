package engine

import (
	"stuntgres/internal/sqltype"
	"testing"
)

func TestDefaultDescriptorProviderClosedDefinitions(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE d(i INT DEFAULT 2147483648,b BOOL DEFAULT (1 < 2))`)
	view := e.CatalogRead()
	ref := roleTableRef(t, e, "d")
	old := view.state.descriptors[ref.Address].(*relationDescriptor)
	if err := (relationProvider{}).Validate(view, old); err != nil {
		t.Fatal("validation evaluated narrowing", err)
	}
	for _, mutate := range []func(*relationDescriptor){
		func(d *relationDescriptor) { d.defaults = map[int32]columnDefault{99: old.defaults[1]} },
		func(d *relationDescriptor) { d.defaults = map[int32]columnDefault{1: old.defaults[2]} },
		func(d *relationDescriptor) {
			bad := old.defaults[1]
			bad.sql = "0"
			d.defaults = map[int32]columnDefault{1: bad}
		},
		func(d *relationDescriptor) {
			u := old.columns[0].Usage
			node := parameterExpression{expressionInfo: expressionInfo{usage: u, properties: ExprProperties{ParameterDependent: true}}, number: 1}
			d.defaults = map[int32]columnDefault{1: {node, "0"}}
		},
		func(d *relationDescriptor) {
			u := old.columns[0].Usage
			node := literalExpression{expressionInfo: expressionInfo{usage: u}, value: sqltype.Null(old.columns[1].Usage.Type)}
			d.defaults = map[int32]columnDefault{1: {node, "NULL"}}
		},
	} {
		next := *old
		mutate(&next)
		if err := (relationProvider{}).Validate(view, &next); err == nil {
			t.Fatal("invalid definition accepted", next.defaults)
		}
	}
}
