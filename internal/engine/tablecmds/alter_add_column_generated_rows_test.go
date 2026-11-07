package tablecmds_test

import (
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestAlterAddColumnGeneratedCompletedRows(t *testing.T) {
	t.Run("final-recipe", func(t *testing.T) {
		e, op, ref := addGeneratedUnitFixture(t)
		store := e.Storage.Relations[ref.Address]
		add := addGeneratedUnitAction("b", "stored", alterGenerationOperator("/", alterGenerationColumn("a"), alterGenerationLiteral(0)))
		set := parser.AlterTableAction{Action: parser.SetExpression, Column: parser.Identifier{Text: "b"}, Generation: &parser.GenerationDefinition{Expression: alterGenerationOperator("+", alterGenerationColumn("a"), alterGenerationLiteral(2))}}
		statement := alterGenerationStatement(add)
		statement.Actions = append(statement.Actions, set)
		if _, err := e.Execute(op, statement, 0, nil); err != nil {
			t.Fatal("rewrite demanded superseded ADD recipe", err)
		}
		next := e.Catalog.State.MustRelation(ref.Address)
		rewritten := e.Storage.Relations[ref.Address]
		if rewritten.Generation != store.Generation+1 || next.ColumnMeta.Generations[2].OID == 0 || len(rewritten.Versions) != 2 {
			t.Fatal("final generation recipe was not one genuine rewrite")
		}
		for _, row := range rewritten.Versions {
			a, ae := row.Values[0].Integer()
			b, be := row.Values[1].Integer()
			if ae != nil || be != nil || b != a+2 {
				t.Fatal("rewrite lost final SET expression", row, ae, be)
			}
		}
	})
	_, scope, _, old := alterAddUnitScope(t)
	u := old.Columns[0].Usage
	integer := func(n int64) sqltype.Value {
		v, err := sqltype.Integer(u.Type, n)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	next := *old
	next.Columns = append(append([]catalog.Column(nil), old.Columns...), catalog.Column{Name: "operand", Usage: u, Nullable: true}, catalog.Column{Name: "b", Usage: u, Nullable: true}, catalog.Column{Name: "v", Usage: u, Nullable: true})
	next.Attributes = []int32{1, 4, 5, 6}
	next.Index = map[string]int{"a": 0, "operand": 1, "b": 2, "v": 3}
	next.ColumnMeta.HighWater = 6
	generated := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(ec *exec.Context) (sqltype.Value, error) {
		if len(ec.Row) != 4 || ec.Row[0] != integer(31) || ec.Row[1] != integer(5) || !ec.Row[3].IsNull() {
			t.Fatal("generation ran before final tuple", ec.Row)
		}
		return integer(36), nil
	}}
	virtual := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(ec *exec.Context) (sqltype.Value, error) {
		if len(ec.Row) != 4 || ec.Row[0] != integer(31) || ec.Row[1] != integer(5) || ec.Row[2] != integer(36) || !ec.Row[3].IsNull() {
			t.Fatal("new virtual recipe did not consume completed rewrite row", ec.Row)
		}
		return integer(62), nil
	}}
	next.ColumnMeta.Generations = map[int32]catalog.ColumnGeneration{5: {Kind: catalog.GenerationStored, Planned: generated}, 6: {Kind: catalog.GenerationVirtual, Planned: virtual}}
	row := func(id ids.RowID, version ids.VersionID) storage.RowVersion {
		return storage.RowVersion{Row: id, Version: version, Born: 1, Values: []sqltype.Value{integer(1)}}
	}
	store := &storage.Relation{Generation: 7, Versions: []storage.RowVersion{row(1, 1), row(2, 2), row(3, 3), row(4, 4)}, Ends: &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{2: {Version: 2, End: 2}}}}
	own := row(3, 5)
	own.Base = 3
	own.Born = 0
	insert := row(5, 6)
	insert.Born = 0
	scope.OwnWrites = map[ids.ObjectAddress][]storage.RowVersion{old.Address(): {own, insert}}
	scope.OwnDeletes = map[ids.ObjectAddress]*visibility.PrivateDeletes{old.Address(): {Rows: map[ids.RowID]visibility.DeleteCandidate{4: {Row: 4, Version: 4}}}}
	transform := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(ec *exec.Context) (sqltype.Value, error) {
		if len(ec.Row) != 1 || ec.Row[0] != integer(1) {
			t.Fatal("TYPE did not consume old tuple")
		}
		return integer(31), nil
	}}
	additions := []table.AddedColumn{{Attribute: 4, Column: &parser.Column{Name: "operand"}, Value: integer(5)}, {Attribute: 5, Column: &parser.Column{Name: "b"}, Value: sqltype.Null(u.Type)}, {Attribute: 6, Column: &parser.Column{Name: "v"}, Value: sqltype.Null(u.Type)}}
	writes, deletes := scope.ColumnProjectionRoots(old.Address())
	replacement, rewritten, remaining, err := table.RewriteTypeRows(tablecmds.AlterRowEvaluation(scope, scope.Op, scope.RoutineAuth.Catalog), old, &next, store, 2, writes, deletes, []index.ColumnTransformation{{Attribute: 1, Expression: transform, Rewrite: true}}, additions, []int32{5}, table.NewAlterRowValidator(&next, "public"), nil)
	if err != nil || replacement.Generation != 8 || len(replacement.Versions) != 1 || len(rewritten) != 2 || remaining != nil {
		t.Fatal(replacement, rewritten, remaining, err)
	}
	if replacement.Versions[0].Row != 1 || rewritten[0].Row != 3 || rewritten[0].Base != 0 || rewritten[1].Row != 5 {
		t.Fatal("rewrite included ended/deleted/predecessor row")
	}
	for _, r := range append(replacement.Versions, rewritten...) {
		if !reflect.DeepEqual(r.Values, []sqltype.Value{integer(31), integer(5), integer(36), sqltype.Null(u.Type)}) {
			t.Fatal(r.Values)
		}
	}
	if store.Generation != 7 || len(store.Versions) != 4 || len(store.Versions[0].Values) != 1 || scope.OwnWrites[old.Address()][0].Base != 3 || len(old.Columns) != 1 {
		t.Fatal("completed rewrite mutated original/private source")
	}
}
