package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestGeneratedVirtualStorageDemandAndFrames(t *testing.T) {
	_, _, d := generatedUnitDescriptor(t)
	typ := d.Columns[0].Usage.Type
	base := columnExpression{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: 1, Slot: 0}
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationVirtual, Expression: base, Planned: base}}
	two, _ := sqltype.Integer(typ, 2)
	seven, _ := sqltype.Integer(typ, 7)
	ten, _ := sqltype.Integer(typ, 10)
	row := []sqltype.Value{two, seven}
	eval := EvalContext{}
	if err := evaluateGenerations(context.Background(), d, row, &eval); err != nil || !row[1].IsNull() || row[1].Type() != typ {
		t.Fatal(row, err)
	}
	generated := columnExpression{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 4, Slot: 2}.WithGeneration(d, 1, 1)
	outer := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 4, Row: []sqltype.Value{ten, two, row[1]}, OldRow: []sqltype.Value{ten, seven, row[1]}}}
	inner := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 8, Outer: &outer}}
	v, err := generated.Eval(&inner)
	if n, e := v.Integer(); err != nil || e != nil || n != 2 {
		t.Fatal(v, err, e)
	}
	v, err = (returningColumnExpression{ColumnExpr: generated, Old: true}).Eval(&inner)
	if n, e := v.Integer(); err != nil || e != nil || n != 7 {
		t.Fatal(v, err, e)
	}
	if outer.Frame != 4 || len(outer.Row) != 3 || inner.Frame != 8 {
		t.Fatal("caller frame changed", outer, inner)
	}
	// An absent side finds its owning frame but never demands a virtual value.
	invalid := base
	invalid.Slot = 99
	generated.Virtual = invalid
	if v, err = (returningColumnExpression{ColumnExpr: generated, Absent: true}).Eval(&inner); err != nil || !v.IsNull() || v.Type() != typ {
		t.Fatal(v, err)
	}
	if _, err = generated.Eval(&inner); err == nil {
		t.Fatal("demand failed to evaluate retained plan")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	inner.Context = canceled
	if _, err = generated.Eval(&inner); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored, Expression: base, Planned: base}}
	if err := evaluateGenerations(context.Background(), d, row, &eval); err != nil {
		t.Fatal(err)
	}
	if n, err := row[1].Integer(); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}

func TestGeneratedVirtualUnsimplifiedTypeSecurity(t *testing.T) {
	_, view, d := generatedUnitDescriptor(t)
	integer := d.Columns[0].Usage.Type
	domain, err := sqltype.Domain(64001, "vd", "public.vd", integer.Usage())
	if err != nil {
		t.Fatal(err)
	}
	array, err := sqltype.Array(64002, "_vd", domain)
	if err != nil {
		t.Fatal(err)
	}
	enum, err := sqltype.Enum(64003, "ve", "public.ve", []string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	enumArray, err := sqltype.Array(64004, "_ve", enum)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		typ    *sqltype.Descriptor
		domain bool
	}{{domain, true}, {array, true}, {enum, false}, {enumArray, false}} {
		err := virtualDestination(catalog.Column{Name: "b", Usage: tc.typ.Usage()})
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" {
			t.Fatal(tc, err)
		}
		if tc.domain {
			if diagnostic.Message != `virtual generated column "b" cannot have a domain type` || diagnostic.Detail != "" {
				t.Fatal(diagnostic)
			}
		} else if diagnostic.Message != `virtual generated column "b" cannot have a user-defined type` || diagnostic.Detail != "Virtual generated columns that make use of user-defined types are not yet supported." {
			t.Fatal(diagnostic)
		}
	}
	builtinArray, ok := view.LookupType(1007)
	if !ok {
		t.Fatal("int[] missing")
	}
	if err := virtualDestination(catalog.Column{Name: "b", Usage: builtinArray.Usage()}); err != nil {
		t.Fatal(err)
	}
	hidden := literalExpression{Info: expr.Info{Usage: domain.Usage(), Pos: parser.Position{Character: 17}}, Value: sqltype.Null(domain)}
	otherwise := literalExpression{Info: expr.Info{Usage: integer.Usage()}, Value: sqltype.Null(integer)}
	tree := caseExpression{Info: expr.Info{Usage: integer.Usage()}, Arms: []caseBranch{{Result: hidden}}, Otherwise: otherwise}
	var diagnostic *sqlstate.Error
	if err := virtualGenerationSecurity(tree); !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Position != "17" || diagnostic.Message != "generation expression uses user-defined type" {
		t.Fatal(err)
	}
	if err := virtualGenerationSecurity(otherwise); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedVirtualReadManifestAndLogicalNullability(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	typ := d.Columns[0].Usage.Type
	base := columnExpression{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, Slot: 0}
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationVirtual, Expression: base, Planned: base}}
	ref := e.catalog.Domain.Ref(view.Epoch(), d.Address())
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	b.query.sources = []queryLeaf{{ref: ref, relation: d}}
	node := b.queryColumnAt(queryColumn{descriptor: d.Columns[1], slot: 1, leaf: 0, physical: 1}, parser.Position{})
	if len(b.query.sources[0].uses) != 1 || b.query.sources[0].uses[0] != 1 {
		t.Fatal("base SELECT widened", b.query.sources[0].uses)
	}
	value, _ := sqltype.Integer(typ, 9)
	row := []sqltype.Value{value, sqltype.Null(typ)}
	source := &querySource{anonymous: [][]sqltype.Value{row}, occurrence: 0}
	state := &queryState{source: source, virtualReads: true}
	eval := state.evaluation(context.Background())
	eval.bindQueryRow(source, 0)
	if v, err := node.Eval(&eval); err != nil || v.IsNull() {
		t.Fatal(v, err)
	}
	d.Columns[1].Nullable = false
	validator := newAlterRowValidator(d, "public")
	if err := validator.validate(row, &eval); err != nil {
		t.Fatal("physical NULL rejected logical nine", err)
	}
	detail, err := failingRowDetail(row, sqltype.OutputEnvironment{}, nil, nil, d)
	if err != nil || detail != "Failing row contains (9, virtual)." {
		t.Fatal(detail, err)
	}
}

func TestGeneratedVirtualReplacementDoesNotMaterializeOwner(t *testing.T) {
	e, view, d := generatedUnitDescriptor(t)
	oid, ok := view.LookupType(sqltype.Oid)
	if !ok {
		t.Fatal("oid missing")
	}
	original := d.Address()
	d.ObjectAddress.ObjectID++
	d.Columns[1].Usage = oid.Usage()
	ref := e.catalog.Domain.Ref(view.Epoch()+1, d.Address())
	node := tableOIDExpression{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, Owner: ref}
	d.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationVirtual, Expression: node, Planned: node}}
	base, _ := sqltype.Integer(d.Columns[0].Usage.Type, 2)
	store := &storage.Relation{Versions: []storage.RowVersion{{Values: []sqltype.Value{base, sqltype.Null(oid)}}}}
	next := catalog.CloneState(view.State())
	next.SetEpoch(next.Epoch() + 1)
	next.SetDescriptor(d.Address(), d)
	if err := recomputeRemappedGenerations(context.Background(), next, map[ids.ObjectAddress]*storage.Relation{d.Address(): store}, map[ids.ObjectAddress]ids.ObjectAddress{original: d.Address()}, EvalContext{}, nil); err != nil {
		t.Fatal(err)
	}
	if !store.Versions[0].Values[1].IsNull() {
		t.Fatal("virtual owner was materialized")
	}
	eval := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1, Row: store.Versions[0].Values}}
	v, err := logicalColumnValue(d, 1, eval.Row, &eval)
	if owner, e := v.OID(); err != nil || e != nil || ids.OID(owner) != d.Address().ObjectID {
		t.Fatal(v, err, e)
	}
}
