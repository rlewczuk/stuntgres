package enginetests_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func matviewsUnitFixture(t *testing.T) (defaultUnitFixture, *catalog.State, *catalog.Relation) {
	t.Helper()
	f := newDefaultUnitFixture(t)
	state := catalog.CloneState(f.e.Catalog.State)
	typ, _ := f.e.CatalogRead().LookupType(sqltype.Int4)
	base := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 22800}, ObjectNamespace: f.schema, ObjectName: "pgmv_base", ObjectRevision: 1, Owner: f.owner, RelationKind: catalog.TableRelation, Columns: []catalog.Column{{Name: "n", Usage: typ.Usage()}}, Attributes: []int32{1}}
	state.SetDescriptor(base.Address(), base)
	ref := f.e.Catalog.Domain.Ref(state.Epoch(), base.Address())
	name := func(s string) parser.ObjectName { return parser.ObjectName{Parts: []parser.Identifier{{Text: s}}} }
	syntax := &parser.Select{From: &parser.TableReference{Name: name("pgmv_base")}, Targets: []parser.Expr{parser.ColumnExpr{Name: name("n")}}}
	source := &plan.SourceNode{QuerySchema: plan.QuerySchema(base.Columns), Ref: ref, Occurrence: 0}
	project := &plan.ProjectNode{QuerySchema: plan.QuerySchema(base.Columns), Child: source, Syntax: syntax, Expressions: []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Origin: ref, Slot: 0}}}
	mv := *base
	mv.ObjectAddress.ObjectID++
	mv.ObjectName = "a"
	mv.RelationKind = catalog.MaterializedViewRelation
	mv.View = stored.NewView(stored.ViewParts{
		Query: &plan.Query{Root: project}, Syntax: syntax,
		Sources: []stored.ViewLeaf{{Ref: ref, Attributes: []int32{1}}},
		Frames: map[uint32]*plan.ProjectNode{0: project},
		Queries: map[*parser.Select]*plan.ProjectNode{syntax: project},
		Columns: map[analyze.ViewLiteralPath]analyze.ViewColumn{{Clause: analyze.ViewTargetLiteral}: {Frame: 0, Slot: 0, Owner: ref}},
		Namespaces: []analyze.ViewNamespace{{Node: analyze.ViewSourceNamespace, Columns: []analyze.ViewNamespaceColumn{{Name: "n", Attribute: 1, Slot: 0, Leaf: 0}}}},
	})
	state.SetDescriptor(mv.Address(), &mv)
	return f, state, &mv
}

func matviewsUnitInput(t *testing.T, f defaultUnitFixture, state *catalog.State) pgcatalog.Input {
	t.Helper()
	view := f.e.Catalog.Read(state)
	auth := dtpUnitAuthority(t, f, view)
	source := view.MustRelation(ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 12006})
	return pgcatalog.Input{Context: t.Context(), Catalog: view, Roles: auth.Roles, Owner: view.Owner(), Ref: view.Owner().Domain.Ref(view.Epoch(), source.Address()), Source: source, Runtime: core.NewCatalogRuntime(core.CatalogRuntimeFields{Auth: auth})}
}

func matviewsUnitCell(t *testing.T, cell sqltype.Value) any {
	t.Helper()
	if cell.Type().ID() == sqltype.Bool && !cell.IsNull() {
		value, err := cell.Boolean()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	return columnsUnitCell(t, cell)
}

func matviewsUnitRows(t *testing.T, in pgcatalog.Input, want [][]any) [][]sqltype.Value {
	t.Helper()
	rows, err := registeredCatalogProducer(12006)(in)
	if err != nil {
		t.Fatal(err)
	}
	got := make([][]any, len(rows))
	for i, row := range rows {
		if len(row) != 7 {
			t.Fatal("row width", len(row))
		}
		got[i] = make([]any, 7)
		for j, cell := range row {
			if cell.Type() != in.Source.Columns[j].Usage.Type || !cell.ValidResult(in.Source.Columns[j].Usage) {
				t.Fatal("cell authority", j, cell)
			}
			got[i][j] = matviewsUnitCell(t, cell)
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("materialized projection", got, want)
	}
	return rows
}

const matviewsUnitDefinition = "SELECT \"n\" AS \"n\" FROM default_schema.pgmv_base;"

func TestPGMatviewsProjection(t *testing.T) {
	f, state, mv := matviewsUnitFixture(t)
	changed := *mv
	changed.Populated = true
	changed.History.HasIndexes = true
	state.SetDescriptor(changed.Address(), &changed)
	// Real ordinary kinds are excluded even when carrying the same view payload.
	for i, kind := range []catalog.RelationKind{catalog.ViewRelation, catalog.IndexRelation, catalog.SequenceRelation} {
		other := *mv
		other.ObjectAddress.ObjectID = ids.OID(22810 + i)
		other.RelationKind = kind
		state.SetDescriptor(other.Address(), &other)
	}
	in := matviewsUnitInput(t, f, state)
	names := []string{"schemaname", "matviewname", "matviewowner", "tablespace", "hasindexes", "ispopulated", "definition"}
	types := []sqltype.ID{19, 19, 19, 19, 16, 16, 25}
	if len(in.Source.Columns) != 7 {
		t.Fatal("source width", len(in.Source.Columns))
	}
	for i, column := range in.Source.Columns {
		typ, _ := in.Catalog.LookupType(types[i])
		if column.Name != names[i] || column.Usage.Type != typ || !column.Nullable {
			t.Fatal("source usage", i, column)
		}
	}
	matviewsUnitRows(t, in, [][]any{{"default_schema", "a", "default_owner", nil, true, true, matviewsUnitDefinition}})
	// Neither a physical inventory nor a live index descriptor is installed.
	unpopulated := *mv
	state.SetDescriptor(unpopulated.Address(), &unpopulated)
	matviewsUnitRows(t, matviewsUnitInput(t, f, state), [][]any{{"default_schema", "a", "default_owner", nil, false, false, matviewsUnitDefinition}})
}

func TestPGMatviewsCapturedRoots(t *testing.T) {
	f, state, mv := matviewsUnitFixture(t)
	old := matviewsUnitInput(t, f, state)
	wantOld := [][]any{{"default_schema", "a", "default_owner", nil, false, false, matviewsUnitDefinition}}
	retained := matviewsUnitRows(t, old, wantOld)
	next := catalog.CloneState(state)
	replacement := *mv
	replacement.ObjectName = "b"
	replacement.Populated = true
	replacement.History.HasIndexes = true
	next.SetDescriptor(replacement.Address(), &replacement)
	matviewsUnitRows(t, matviewsUnitInput(t, f, next), [][]any{{"default_schema", "b", "default_owner", nil, true, true, matviewsUnitDefinition}})
	matviewsUnitRows(t, old, wantOld)
	for i, cell := range retained[0] {
		if !reflect.DeepEqual(matviewsUnitCell(t, cell), wantOld[0][i]) {
			t.Fatal("retained captured cell changed", i, cell)
		}
	}
	if mv.Name() != "a" || mv.Populated || mv.History.HasIndexes || old.Catalog.Get(mv.Address()) != mv {
		t.Fatal("retained descriptor changed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	old.Context = ctx
	if rows, err := registeredCatalogProducer(12006)(old); rows != nil || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled capture published rows", rows, err)
	}
}
