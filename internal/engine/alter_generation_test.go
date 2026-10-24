package engine

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func alterGenerationAction(expr parser.Expr) parser.AlterTableAction {
	return parser.AlterTableAction{Action: parser.SetExpression, Column: parser.Identifier{Text: "b"}, Generation: &parser.GenerationDefinition{Expression: expr}}
}

func alterGenerationLiteral(n int64) parser.Expr {
	return parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: n}}
}

func alterGenerationStatement(action parser.AlterTableAction) *parser.AlterTable {
	return &parser.AlterTable{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "g"}}}, Actions: []parser.AlterTableAction{action}}
}

func TestAlterGeneratedExpressionCandidate(t *testing.T) {
	e, admin, _, ref := generatedAuthorizationFixture(t)
	elements := []parser.TableElement{{Column: &parser.Column{Name: "a", TypeName: parser.TypeName{Name: "int4"}}}}
	for i, name := range []string{"b", "c"} {
		expr := alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationLiteral(int64(i+1)))
		if name == "c" {
			expr = alterGenerationLiteral(17)
		}
		elements = append(elements, parser.TableElement{Column: &parser.Column{Name: name, TypeName: parser.TypeName{Name: "int4"}, Constraints: []parser.ColumnConstraint{{Generation: &parser.GenerationDefinition{Mode: parser.IdentityAlways, Kind: "stored", Expression: expr}}}}})
	}
	if _, err := e.Execute(admin, &parser.CreateTable{Name: alterGenerationName("g2"), Elements: elements}, 0, nil); err != nil {
		t.Fatal(err)
	}
	ref = catalogRef(t, e, e.CatalogRead(), "g2")
	auth, err := operationAuthorization(admin)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := e.StartStatement(admin)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(errors.New("candidate-only unit"))
	old := e.catalog.State.MustRelation(ref.Address)
	original := old.ColumnMeta.Generations[2]
	retained := auth.Catalog
	unaffected := old.ColumnMeta.Generations[3]
	next, attribute, err := e.generationCandidate(scope, admin, auth, old, alterGenerationAction(alterGenerationLiteral(8)))
	if err != nil || attribute != 2 || next == old || next.ColumnMeta.Generations[2].OID != 0 || next.ColumnMeta.Generations[2].Kind != catalog.GenerationStored {
		t.Fatal(next, attribute, err)
	}
	e.mu.Lock()
	err = table.AssignDefinitionOIDs(ddlHost{e: e}, next)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if next.ColumnMeta.Generations[2].OID == 0 || next.ColumnMeta.Generations[2].OID == original.OID || !reflect.DeepEqual(old.ColumnMeta.Generations[2], original) || retained.Get(ref.Address) != old || !slices.Equal(old.Attributes, next.Attributes) || &old.Columns[0] != &next.Columns[0] {
		t.Fatal("source/shape identity changed")
	}
	if next.ColumnMeta.Generations[3].OID != unaffected.OID || !reflect.DeepEqual(next.ColumnMeta.Generations[3], unaffected) || !reflect.DeepEqual(old.ColumnMeta.Generations[3], unaffected) {
		t.Fatal("unaffected definition reminted/mutated")
	}
	drop := parser.AlterTableAction{Action: parser.DropExpression, Column: parser.Identifier{Text: "b"}}
	dropped, changed, err := e.generationCandidate(scope, admin, auth, next, drop)
	if err != nil || changed != 0 || len(dropped.ColumnMeta.Generations) != 1 || len(next.ColumnMeta.Generations) != 2 || dropped.Columns[1] != old.Columns[1] || dropped.ColumnMeta.Generations[3].OID != unaffected.OID {
		t.Fatal(dropped, changed, err)
	}
	target := ref.Address
	target.SubID = 1
	originalDrop, _, err := e.generationCandidate(scope, admin, auth, old, drop)
	if err != nil || !table.GenerationDepends(old, target) || table.GenerationDepends(next, target) || table.GenerationDepends(originalDrop, target) || originalDrop.ColumnMeta.Generations[3].OID != unaffected.OID {
		t.Fatal("SET/DROP dependency edges or unaffected definition wrong", err)
	}
	drop.IfExists = true
	same, _, err := e.generationCandidate(scope, admin, auth, dropped, drop)
	if err != nil || same != dropped || same.Revision() != dropped.Revision() {
		t.Fatal("no-op changed descriptor", err)
	}
	if e.catalog.State.Get(ref.Address) != old {
		t.Fatal("candidate published early")
	}
}

func TestAlterGeneratedExpressionCompletedRow(t *testing.T) {
	_, scope, _, base := alterAddUnitScope(t)
	u := base.Columns[0].Usage
	integer := func(n int64) sqltype.Value {
		t.Helper()
		v, err := sqltype.Integer(u.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	old := *base
	old.Columns = []catalog.Column{{Name: "a", Usage: u, Nullable: true}, {Name: "b", Usage: u, Nullable: true}, {Name: "untouched", Usage: u, Nullable: true}}
	old.Attributes = []int32{1, 2, 3}
	old.Index = map[string]int{"a": 0, "b": 1, "untouched": 2}
	next := old
	next.Columns = slices.Clone(old.Columns)
	next.Columns = append(next.Columns, catalog.Column{Name: "c", Usage: u, Nullable: true})
	next.Attributes = []int32{1, 2, 3, 4}
	next.ColumnMeta.HighWater = 4
	next.Index = maps.Clone(old.Index)
	next.Index["c"] = 3
	calls := 0
	generated := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(ec *exec.Context) (sqltype.Value, error) {
		calls++
		if len(ec.Row) != 4 || ec.Row[0] != integer(31) || ec.Row[3] != integer(5) {
			t.Fatal("generation evaluated before completed tuple", ec.Row)
		}
		return integer(36), nil
	}}
	untouched := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(*exec.Context) (sqltype.Value, error) {
		t.Fatal("unaffected stored generation demanded")
		return sqltype.Value{}, errors.New("unexpected evaluation")
	}}
	old.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored, Planned: untouched}, 3: {Kind: catalog.GenerationStored, Planned: untouched}}
	next.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored, Planned: generated}, 3: {Kind: catalog.GenerationStored, Planned: untouched}}
	row := func(id ids.RowID, version uint64) storage.RowVersion {
		return storage.RowVersion{Row: id, Version: ids.VersionID(version), Born: 1, Values: []sqltype.Value{integer(1), integer(2), integer(99)}}
	}
	store := &storage.Relation{Generation: 1, Versions: []storage.RowVersion{row(1, 1), row(2, 2), row(3, 3), row(4, 4)}, Ends: &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{2: {Version: 2, End: 2}}}}
	own := row(3, 5)
	own.Base = 3
	own.Born = 0
	insert := row(5, 6)
	insert.Born = 0
	scope.ownWrites = map[ids.ObjectAddress][]storage.RowVersion{old.Address(): {own, insert}}
	scope.ownDeletes = map[ids.ObjectAddress]*visibility.PrivateDeletes{old.Address(): {Rows: map[ids.RowID]visibility.DeleteCandidate{4: {Row: 4, Version: 4}}}}
	transform := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(ec *exec.Context) (sqltype.Value, error) {
		if len(ec.Row) != 3 || ec.Row[0] != integer(1) {
			t.Fatal("TYPE lost old row", ec.Row)
		}
		return integer(31), nil
	}}
	additions := []table.AddedColumn{{Attribute: 4, Column: &parser.Column{Name: "c"}, Value: integer(5)}}
	ownWrites, ownDeletes := scope.columnProjectionRoots(old.Address())
	replacement, writes, deletes, err := table.RewriteTypeRows(alterRowEvaluation(scope, scope.op, scope.routineAuth.Catalog), &old, &next, store, 2, ownWrites, ownDeletes, []index.ColumnTransformation{{Attribute: 1, Expression: transform, Rewrite: true}}, additions, []int32{2}, table.NewAlterRowValidator(&next, ""))
	if err != nil || calls != 3 || len(replacement.Versions) != 1 || len(writes) != 2 || deletes != nil || replacement.Generation != 2 {
		t.Fatal(replacement, writes, calls, err)
	}
	if replacement.Versions[0].Row != 1 || writes[0].Row != 3 || writes[0].Base != 0 || writes[1].Row != 5 {
		t.Fatal("ended/deleted/predecessor resurrected", replacement, writes)
	}
	for _, r := range append(slices.Clone(replacement.Versions), writes...) {
		if !slices.Equal(r.Values, []sqltype.Value{integer(31), integer(36), integer(99), integer(5)}) {
			t.Fatal(r.Values)
		}
	}
	for _, r := range store.Versions {
		if len(r.Values) != 3 || r.Values[0] != integer(1) || r.Values[1] != integer(2) || r.Values[2] != integer(99) {
			t.Fatal("source changed", r)
		}
	}
	if scope.ownWrites[old.Address()][0].Base != 3 || scope.ownWrites[old.Address()][0].Values[1] != integer(2) {
		t.Fatal("own source changed")
	}
}

func TestAlterGeneratedExpressionCancellation(t *testing.T) {
	e, scope, _, base := alterAddUnitScope(t)
	u := base.Columns[0].Usage
	value, err := sqltype.Integer(u.Type, 2)
	if err != nil {
		t.Fatal(err)
	}
	old := *base
	old.Columns = append(slices.Clone(base.Columns), catalog.Column{Name: "b", Usage: u, Nullable: true})
	old.Attributes = []int32{1, 2}
	old.Index = map[string]int{"a": 0, "b": 1}
	old.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored}}
	next := old
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	expression := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(ec *exec.Context) (sqltype.Value, error) {
		calls++
		if !e.mu.TryLock() {
			t.Fatal("generation evaluation held Engine.mu")
		}
		e.mu.Unlock()
		cancel()
		return value, nil
	}}
	next.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{2: {Kind: catalog.GenerationStored, Planned: expression}}
	store := &storage.Relation{Generation: 7, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value, value}}, {Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{value, value}}}}
	op, cleanup, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	ownWrites, ownDeletes := scope.columnProjectionRoots(old.Address())
	replacement, writes, deletes, err := table.RewriteTypeRows(alterRowEvaluation(scope, op, scope.routineAuth.Catalog), &old, &next, store, 1, ownWrites, ownDeletes, nil, nil, []int32{2}, table.NewAlterRowValidator(&next, ""))
	if !errors.Is(err, context.Canceled) || calls != 1 || replacement != nil || writes != nil || deletes != nil || store.Generation != 7 || len(store.Versions[0].Values) != 2 || store.Versions[0].Values[1] != value {
		t.Fatal("real rewrite cancellation leaked candidate", replacement, writes, deletes, calls, err)
	}
}
