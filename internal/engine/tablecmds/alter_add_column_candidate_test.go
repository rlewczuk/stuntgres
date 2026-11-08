package tablecmds_test

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/engine/visibility"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func alterAddUnitScope(t *testing.T) (*core.Engine, *core.StatementScope, core.AuthorizationView, *catalog.Relation) {
	t.Helper()
	e := engine.New()
	op, done := roleOperation(t, e, "postgres")
	t.Cleanup(done)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = scope.Finish(errors.New("unit candidate discarded")) })
	auth, err := core.OperationAuthorization(op)
	if err != nil {
		t.Fatal(err)
	}
	u := enginetest.Usage(e, sqltype.Int4)
	old := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "t", ObjectRevision: 1, Owner: auth.State.CurrentUser, Columns: []catalog.Column{{Name: "a", Usage: u, Nullable: true, NullabilityKnown: true, OriginTable: 20000, OriginAttribute: 1}}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 3}, Index: map[string]int{"a": 0}}
	old.TableMethod = 2
	return e, scope, auth, old
}

func alterAddUnitAction(t *testing.T, query string) parser.AlterTableAction {
	t.Helper()
	statements, err := parser.Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	return statements[0].(*parser.AlterTable).Actions[0]
}

func TestAlterAddColumnOwnedCandidateAndHighWater(t *testing.T) {
	e, scope, auth, old := alterAddUnitScope(t)
	action := alterAddUnitAction(t, "ALTER TABLE t ADD b INT DEFAULT 7 NOT NULL CHECK(b>a)")
	next, added, err := tablecmds.AddColumnCandidate(e, scope, scope.Op, auth, old, action, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.ColumnMeta.HighWater != 4 || !reflect.DeepEqual(next.Attributes, []int32{1, 4}) || next.Columns[1].OriginAttribute != 4 || next.Columns[1].OriginTable != 20000 || added.Attribute != 4 || len(old.Columns) != 1 || old.ColumnMeta.HighWater != 3 || len(old.ColumnMeta.Defaults) != 0 {
		t.Fatal("stable identities/source changed", old, next, added)
	}
	if &next.Columns[0] == &old.Columns[0] || &next.Attributes[0] == &old.Attributes[0] {
		t.Fatal("candidate shares mutable shape")
	}
	next.Index["sentinel"] = 2
	if _, exists := old.Index["sentinel"]; exists {
		t.Fatal("candidate shares index")
	}
	delete(next.Index, "sentinel")
	fills, volatile, err := table.PlanAddedColumns(tablecmds.AlterRowEvaluation(scope, scope.Op, scope.RoutineAuth.Catalog), []table.AddedColumn{added})
	if err != nil || volatile {
		t.Fatal(fills, volatile, err)
	}
	value, err := fills[4].Integer()
	if err != nil || value != 7 {
		t.Fatal(value, err)
	}
	// The inline PRIMARY KEY reaches the candidate: the declaration implies
	// NOT NULL, its raw constraint stays for index staging, and the source
	// relation is never mutated.
	primary := alterAddUnitAction(t, "ALTER TABLE t ADD b INT PRIMARY KEY")
	nextPK, addedPK, err := tablecmds.AddColumnCandidate(e, scope, scope.Op, auth, old, primary, nil)
	if err != nil {
		t.Fatal(err)
	}
	if nextPK.ColumnMeta.HighWater != 4 || addedPK.Attribute != 4 || !addedPK.Declaration.ColumnNotNull || addedPK.Declaration.ColumnName != "" || addedPK.Column == nil || len(addedPK.Column.Constraints) != 1 || addedPK.Column.Constraints[0].Unique == nil || !addedPK.Column.Constraints[0].Unique.Primary {
		t.Fatal("inline PRIMARY KEY candidate shape", nextPK, addedPK)
	}
	if len(old.Columns) != 1 || old.ColumnMeta.HighWater != 3 || len(old.Index) != 1 {
		t.Fatal("inline PRIMARY KEY mutated the source relation", old)
	}
	conflictQuery := "ALTER TABLE t ADD b INT NULL PRIMARY KEY"
	conflict := alterAddUnitAction(t, conflictQuery)
	_, _, err = tablecmds.AddColumnCandidate(e, scope, scope.Op, auth, old, conflict, nil)
	var conflictErr *sqlstate.Error
	if !errors.As(err, &conflictErr) || conflictErr.Code != "42601" || conflictErr.Message != `conflicting NULL/NOT NULL declarations for column "b" of table "t"` || conflictErr.Position != strconv.Itoa(strings.Index(conflictQuery, "PRIMARY")+1) {
		t.Fatal(err)
	}
	// A syntactically valid skipped declaration never resolves its unknown type.
	skip := alterAddUnitAction(t, "ALTER TABLE t ADD IF NOT EXISTS a nonexistent_type DEFAULT 'oops'")
	same, ignored, err := tablecmds.AddColumnCandidate(e, scope, scope.Op, auth, old, skip, nil)
	if err != nil || same != old || ignored.Column != nil || old.ColumnMeta.HighWater != 3 {
		t.Fatal(same, ignored, err)
	}
	limit := *old
	limit.ColumnMeta.HighWater = 1600
	_, _, err = tablecmds.AddColumnCandidate(e, scope, scope.Op, auth, &limit, action, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "54011" || limit.ColumnMeta.HighWater != 1600 {
		t.Fatal(err)
	}
}

func TestAlterAddColumnNonvolatileDemandOnce(t *testing.T) {
	_, scope, _, old := alterAddUnitScope(t)
	u := old.Columns[0].Usage
	value, err := sqltype.Integer(u.Type, 7)
	if err != nil {
		t.Fatal(err)
	}
	for _, volatility := range []expr.Volatility{expr.Immutable, expr.Stable} {
		for _, innerDomain := range []bool{false, true} {
			calls := 0
			references := []ids.ObjectRef{scope.RoutineAuth.Catalog.Domain().Ref(scope.RoutineAuth.Catalog.Epoch(), old.Address())}
			programs := map[*catalog.Type]exec.DomainCheckProgram{nil: {}}
			expression := queryProbeExpression{Info: expr.Info{Usage: u, Property: expr.Properties{Volatility: volatility, RuntimeDomainConversion: innerDomain}}, eval: func(c *exec.Context) (sqltype.Value, error) {
				calls++
				if calls == 1 && (!c.RoutineRecipe || c.RoutineGraft || c.RoutineConsumer != old || !reflect.DeepEqual(c.RoutineReferences, references)) {
					t.Fatal("ADD evaluation lost its admitted stored recipe")
				}
				if _, exists := c.PreparedDomainChecks[nil]; !exists {
					t.Fatal("ADD evaluation lost prepared domain programs")
				}
				return value, nil
			}}
			additions := []table.AddedColumn{{Attribute: 4, Column: &parser.Column{Name: "b"}, Expression: expression, Dependencies: references, Consumer: old, Value: sqltype.Null(u.Type)}}
			evaluation := tablecmds.AlterRowEvaluation(scope, scope.Op, scope.RoutineAuth.Catalog)
			evaluation.RoutineGraft = true
			evaluation.PreparedDomainChecks = programs
			fills, volatile, err := table.PlanAddedColumns(evaluation, additions)
			if err != nil || volatile || calls != 1 || fills[4] != value || additions[0].Value != value {
				t.Fatal(fills, volatile, calls, err)
			}
			// Empty backfill does not suppress the earlier one-time demand.
			if rows, err := session.ProjectVersions(context.Background(), nil, old, old, fills); err != nil || len(rows) != 0 || calls != 1 {
				t.Fatal(rows, calls, err)
			}
			// A companion's genuine rewrite demands runtime domain conversions
			// again per live row, matching PostgreSQL rather than using its fill.
			next := *old
			next.Columns = append(append([]catalog.Column(nil), old.Columns...), catalog.Column{Name: "b", Usage: u, Nullable: true})
			next.Attributes, next.Index = []int32{1, 4}, map[string]int{"a": 0, "b": 1}
			next.ColumnMeta.HighWater = 4
			store := &storage.Relation{Generation: 1, Versions: []storage.RowVersion{
				{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{value}},
				{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{value}},
				{Row: 3, Version: 3, Born: 1, Values: []sqltype.Value{value}},
			}, Ends: &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{3: {Version: 3, End: 2}}}}
			rewritten, own, deleted, err := table.RewriteTypeRows(evaluation, old, &next, store, 2, nil, nil, nil, additions, nil, table.NewAlterRowValidator(&next, "public"), nil)
			wantCalls := 1
			if innerDomain {
				wantCalls += 2
			}
			if err != nil || calls != wantCalls || len(rewritten.Versions) != 2 || len(own) != 0 || deleted != nil {
				t.Fatal("companion rewrite changed domain demand", rewritten, own, deleted, calls, err)
			}
			for i, row := range rewritten.Versions {
				if row.Row != ids.RowID(i+1) || row.Version != ids.VersionID(i+1) || !reflect.DeepEqual(row.Values, []sqltype.Value{value, value}) {
					t.Fatal("companion rewrite lost value or identity", row)
				}
			}
			if len(store.Versions) != 3 || len(store.Versions[0].Values) != 1 || len(old.Columns) != 1 {
				t.Fatal("companion rewrite mutated predecessor")
			}
			if expression.Property.Volatility != volatility || expression.Property.RuntimeDomainConversion != innerDomain || !evaluation.RoutineGraft || evaluation.RoutineRecipe {
				t.Fatal("stored expression or caller context changed")
			}
		}
	}
}

func TestAlterAddColumnConstrainedDestinationKeepsRowDemand(t *testing.T) {
	_, scope, _, old := alterAddUnitScope(t)
	base := old.Columns[0].Usage
	for _, checks := range []bool{false, true} {
		domain, err := sqltype.Domain(20001, "added_domain", "public.added_domain", base, checks, !checks)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		expression := queryProbeExpression{Info: expr.Info{Usage: domain.Usage(), Property: expr.Properties{RuntimeDomainConversion: true}}, eval: func(*exec.Context) (sqltype.Value, error) {
			calls++
			return sqltype.Null(domain), nil
		}}
		additions := []table.AddedColumn{{Attribute: 4, Expression: expression, Value: sqltype.Null(domain)}}
		fills, perRow, err := table.PlanAddedColumns(tablecmds.AlterRowEvaluation(scope, scope.Op, scope.RoutineAuth.Catalog), additions)
		if err != nil || !perRow || calls != 0 || len(fills) != 0 {
			t.Fatal("destination constraints lost row demand", fills, perRow, calls, err)
		}
		if !expression.Property.RuntimeDomainConversion || additions[0].Value.Type() != domain {
			t.Fatal("destination conversion authority changed")
		}
	}
}

func TestAlterAddColumnVolatileEffectiveLiveRewrite(t *testing.T) {
	_, scope, _, old := alterAddUnitScope(t)
	u := old.Columns[0].Usage
	one, err := sqltype.Integer(u.Type, 1)
	if err != nil {
		t.Fatal(err)
	}
	next := *old
	next.Columns = append(append([]catalog.Column(nil), old.Columns...), catalog.Column{Name: "b", Usage: u, Nullable: true})
	next.Attributes = []int32{1, 4}
	next.Index = map[string]int{"a": 0, "b": 1}
	next.ColumnMeta.HighWater = 4
	calls := 0
	expression := queryProbeExpression{Info: expr.Info{Usage: u, Property: expr.Properties{Volatility: expr.Volatile}}, eval: func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Integer(u.Type, int64(calls)) }}
	additions := []table.AddedColumn{{Attribute: 4, Column: &parser.Column{Name: "b"}, Expression: expression, Value: sqltype.Null(u.Type)}}
	fills, volatile, err := table.PlanAddedColumns(tablecmds.AlterRowEvaluation(scope, scope.Op, scope.RoutineAuth.Catalog), additions)
	if err != nil || !volatile || calls != 0 || len(fills) != 0 {
		t.Fatal(fills, volatile, calls, err)
	}
	store := &storage.Relation{Generation: 1, Versions: []storage.RowVersion{
		{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{one}},
		{Row: 2, Version: 2, Born: 1, Values: []sqltype.Value{one}},
		{Row: 3, Version: 3, Born: 1, Values: []sqltype.Value{one}},
		{Row: 4, Version: 4, Born: 1, Values: []sqltype.Value{one}},
	}, Ends: &storage.DeletionRoot{Rows: map[ids.RowID]storage.RowEnd{2: {Version: 2, End: 2}}}}
	scope.OwnWrites = map[ids.ObjectAddress][]storage.RowVersion{old.Address(): {{Row: 3, Version: 5, Base: 3, Values: []sqltype.Value{one}}, {Row: 5, Version: 6, Values: []sqltype.Value{one}}}}
	scope.OwnDeletes = map[ids.ObjectAddress]*visibility.PrivateDeletes{old.Address(): {Rows: map[ids.RowID]visibility.DeleteCandidate{4: {Row: 4, Version: 4}}}}
	transformCalls := 0
	transformed, err := sqltype.Integer(u.Type, 31)
	if err != nil {
		t.Fatal(err)
	}
	transform := queryProbeExpression{Info: expr.Info{Usage: u}, eval: func(ec *exec.Context) (sqltype.Value, error) {
		transformCalls++
		if len(ec.Row) != 1 || ec.Row[0] != one {
			t.Fatal("TYPE did not read original frame", ec.Row)
		}
		return transformed, nil
	}}
	transforms := []index.ColumnTransformation{{Attribute: 1, Expression: transform, Rewrite: true}}
	ownWrites, ownDeletes := scope.ColumnProjectionRoots(old.Address())
	replacement, own, deleted, err := table.RewriteTypeRows(tablecmds.AlterRowEvaluation(scope, scope.Op, scope.RoutineAuth.Catalog), old, &next, store, 2, ownWrites, ownDeletes, transforms, additions, nil, table.NewAlterRowValidator(&next, ""), nil)
	if err != nil || calls != 3 || len(replacement.Versions) != 1 || len(own) != 2 || deleted != nil {
		t.Fatal(replacement, own, calls, err)
	}
	if replacement.Versions[0].Row != 1 || own[0].Row != 3 || own[0].Base != 0 || own[1].Row != 5 || len(store.Versions) != 4 || len(store.Versions[0].Values) != 1 || replacement.Generation != 2 {
		t.Fatal("identity/source rewrite invariant", replacement, own, store)
	}
	if transformCalls != 3 {
		t.Fatal("TYPE effective demand", transformCalls)
	}
	for i, row := range append(append([]storage.RowVersion(nil), replacement.Versions...), own...) {
		expectedDraw, drawErr := sqltype.Integer(u.Type, int64(i+1))
		if drawErr != nil {
			t.Fatal(drawErr)
		}
		if len(row.Values) != 2 || row.Values[0] != transformed || row.Values[1] != expectedDraw {
			t.Fatal("complete transformed/add frame", row.Values)
		}
	}
	for _, row := range store.Versions {
		if len(row.Values) != 1 || row.Values[0] != one {
			t.Fatal("committed predecessor mutated", row)
		}
	}
	for _, row := range scope.OwnWrites[old.Address()] {
		if len(row.Values) != 1 || row.Values[0] != one {
			t.Fatal("private predecessor mutated", row)
		}
	}
}

func roleOperation(t *testing.T, e *core.Engine, name string) (*session.Operation, func()) {
	t.Helper()
	ref, err := e.ResolveLogin(name)
	if err != nil {
		t.Fatal(err)
	}
	s, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: enginetest.NextSessionID()}, ref)
	if err != nil {
		t.Fatal(err)
	}
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return op, func() {
		op.Done()
		s.RequestEnd(session.CleanClose)
		if err := s.WaitEnd(context.Background()); err != nil {
			t.Error(err)
		}
	}
}
