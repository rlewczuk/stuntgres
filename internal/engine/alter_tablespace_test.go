package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestAlterTablespacePreparation(t *testing.T) {
	e, scope, auth, old := alterAddUnitScope(t)
	ref := e.catalog.domain.Ref(auth.catalog.Epoch(), old.address)
	prepare := func(actions []parser.AlterTableAction) (alterTablePreparation, error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.prepareAlterTableActions(scope, scope.op, auth, old, ref, actions)
	}
	action := func(name string) parser.AlterTableAction {
		return parser.AlterTableAction{Action: parser.SetTablespace, Tablespace: parser.Identifier{Text: name}}
	}
	missingType := parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "absent"}}
	for _, tc := range []struct {
		actions       []parser.AlterTableAction
		code, message string
	}{
		{[]parser.AlterTableAction{action("missing")}, "42704", `tablespace "missing" does not exist`},
		{[]parser.AlterTableAction{action("pg_default"), action("missing")}, "42704", `tablespace "missing" does not exist`},
		{[]parser.AlterTableAction{action("pg_default"), action("pg_default")}, "42601", "cannot have multiple SET TABLESPACE subcommands"},
		{[]parser.AlterTableAction{action("pg_global"), action("pg_default")}, "42601", "cannot have multiple SET TABLESPACE subcommands"},
		{[]parser.AlterTableAction{missingType, action("missing")}, "42703", `column "absent" of relation "t" does not exist`},
		{[]parser.AlterTableAction{action("missing"), missingType}, "42704", `tablespace "missing" does not exist`},
		{[]parser.AlterTableAction{action("pg_global"), missingType}, "42703", `column "absent" of relation "t" does not exist`},
		{[]parser.AlterTableAction{missingType, action("pg_global")}, "42703", `column "absent" of relation "t" does not exist`},
	} {
		_, err := prepare(tc.actions)
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != tc.code || got.Message != tc.message {
			t.Fatal(tc, err)
		}
	}
	add := parser.AlterTableAction{Action: parser.AddColumn, Column: parser.Identifier{Text: "a"}}
	original := []parser.AlterTableAction{action("pg_default"), add}
	prepared, err := prepare(original)
	work, destination := prepared.actions, prepared.destination
	if err != nil || !reflect.DeepEqual(work, []parser.AlterTableAction{add}) || original[0].Action != parser.SetTablespace {
		t.Fatal(work, err, original)
	}
	if err := validateAlterTablespaceMove(destination); err != nil {
		t.Fatal(err)
	}
	prepared, err = prepare([]parser.AlterTableAction{action("pg_global"), add})
	work, destination = prepared.actions, prepared.destination
	if err != nil || !reflect.DeepEqual(work, []parser.AlterTableAction{add}) {
		t.Fatal("movement was not deferred", work, err)
	}
	var movement *sqlstate.Error
	if err := validateAlterTablespaceMove(destination); !errors.As(err, &movement) || movement.Code != "22023" || movement.Message != "only shared relations can be placed in pg_global tablespace" {
		t.Fatal(err)
	}
	// An earlier ADD must not invent a TYPE target in the original descriptor.
	addedType := parser.AlterTableAction{Action: parser.AlterColumnType, Column: parser.Identifier{Text: "new_column"}}
	added := parser.AlterTableAction{Action: parser.AddColumn, Column: addedType.Column}
	if _, err := prepare([]parser.AlterTableAction{added, addedType, action("missing")}); !errors.As(err, &movement) || movement.Code != "42703" {
		t.Fatal(err)
	}
	for _, kind := range []relationKind{tableRelation, partitionedRelation, indexRelation, materializedViewRelation} {
		if err := alterTablespaceKind(&relationDescriptor{RelationKind: kind}); err != nil {
			t.Fatal(kind, err)
		}
	}
	for _, kind := range []relationKind{viewRelation, sequenceRelation, foreignRelation} {
		err := alterTablespaceKind(&relationDescriptor{name: "t", RelationKind: kind})
		var got *sqlstate.Error
		if !errors.As(err, &got) || got.Code != "42809" || got.Detail == "" {
			t.Fatal(kind, err)
		}
	}
}

func TestAlterTablespaceNoop(t *testing.T) {
	e, candidateScope, auth, old := alterAddUnitScope(t)
	before, roles, store, revision, epoch := e.catalog.state, e.roles, e.storage[old.address], old.revision, e.catalog.state.epoch
	actions := []parser.AlterTableAction{{Action: parser.SetTablespace, Tablespace: parser.Identifier{Text: "pg_default"}}}
	e.mu.Lock()
	prepared, err := e.prepareAlterTableActions(candidateScope, candidateScope.op, auth, old, e.catalog.domain.Ref(auth.catalog.Epoch(), old.address), actions)
	e.mu.Unlock()
	work, destination := prepared.actions, prepared.destination
	if err != nil || len(work) != 0 || e.catalog.state != before || e.roles != roles || e.storage[old.address] != store || old.revision != revision || e.catalog.state.epoch != epoch {
		t.Fatal("default preparation changed authority/storage", work, err)
	}
	if err := validateAlterTablespaceMove(destination); err != nil {
		t.Fatal(err)
	}
	if inheritanceRecursiveAction(old, actions[0], false) || !tablespaceOnlyActions(actions) {
		t.Fatal("assignment propagated to descendants")
	}
	// A successful no-work utility must still pass the ordinary completion gate.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	op, done, err := testOperation(e, ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.worked = true
	cancel()
	if err := scope.Finish(nil); !errors.Is(err, context.Canceled) {
		t.Fatal("no-op bypassed cancellation", err)
	}
}

func TestAlterTablespaceTypeCapture(t *testing.T) {
	e, scope, auth, old := alterAddUnitScope(t)
	ref := e.catalog.domain.Ref(auth.catalog.Epoch(), old.address)
	before, roles, revision := e.catalog.state, e.roles, old.revision
	prepare := func(query string) (alterTablePreparation, error) {
		statements, err := parser.Parse(query)
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.prepareAlterTableActions(scope, scope.op, auth, old, ref, statements[0].(*parser.AlterTable).Actions)
	}
	for _, tc := range []struct {
		clause, code string
	}{
		{"ALTER a TYPE ats_missing_type", "42704"},
		{"ALTER a TYPE INT USING absent", "42703"},
		{"ALTER a TYPE INT USING a::text", "42804"},
		{`ALTER a TYPE INT COLLATE "C"`, "42804"},
		{"ALTER a TYPE TEXT COLLATE ats_missing_collation", "42704"},
	} {
		for _, first := range []bool{true, false} {
			query := "ALTER TABLE t " + tc.clause + ", SET TABLESPACE ats_missing"
			if !first {
				query = "ALTER TABLE t SET TABLESPACE ats_missing, " + tc.clause
			}
			_, err := prepare(query)
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) {
				t.Fatal(query, err)
			}
			if first && diagnostic.Code != tc.code || !first && (diagnostic.Code != "42704" || diagnostic.Message != `tablespace "ats_missing" does not exist`) {
				t.Fatal(query, diagnostic)
			}
			if e.catalog.state != before || e.roles != roles || old.revision != revision || old.Columns[0].Usage.Type.ID() != sqltype.Int4 {
				t.Fatal("preparation published state", query)
			}
		}
	}
	// Both occurrences are cooked against the original INT4 row, without Eval:
	// the second expression is valid to bind but fails only on row execution.
	prepared, err := prepare("ALTER TABLE t ALTER a TYPE BIGINT USING a, SET TABLESPACE pg_default, ALTER a TYPE BIGINT USING 10/(a-a)")
	if err != nil || len(prepared.types) != 2 || len(prepared.actions) != 2 {
		t.Fatal(prepared, err)
	}
	for _, captured := range prepared.types {
		if captured.usage.Type.ID() != sqltype.Int8 || captured.transformation.attribute != old.Attributes[0] || captured.transformation.expression == nil || !captured.transformation.rewrite {
			t.Fatal("incomplete TYPE capture", captured)
		}
	}
	// The candidate consumes the completed expression; it must not consult a
	// second raw USING graph. This is the actual mutation seam, not a binder mock.
	action := prepared.actions[0]
	action.Using = alterAddUnitAction(t, "ALTER TABLE t ALTER a TYPE BIGINT USING absent").Using
	next, consumed, err := e.alterTypeCandidate(scope, scope.op, auth, nil, old, old, ref, action, prepared.types[0])
	if err != nil || next.Columns[0].Usage.Type.ID() != sqltype.Int8 || !reflect.DeepEqual(consumed, prepared.types[0].transformation) || old.Columns[0].Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("candidate did not consume capture", next, consumed, err)
	}
}

func TestAlterTablespaceInheritanceTypeCapture(t *testing.T) {
	e, parent, child := inheritanceDMLPublishedGraph(t)
	before := e.CatalogRead()
	command := inheritanceAlterAdmissionScope(t, e, "postgres", "ALTER TABLE p ALTER i TYPE BIGINT USING i+1, SET TABLESPACE pg_default")
	// Bound column origins identify an attribute, not just its relation.
	// Keep the complete admitted identity, including owner and catalog epoch.
	rootColumn := command.scope.lockedRef
	rootColumn.Address.SubID = parent.Attributes[0]
	holder := inheritanceAlterAXHolder(t, e, child)
	result, joined := inheritanceAlterWorker(t, command, holder)
	state := inheritanceAlterPending(t, command, child)
	if len(state.types) != 1 || state.tablespace.Address.ObjectID != 1663 {
		t.Fatal("root lost original preparation", state.types, state.tablespace)
	}
	rootCapture := state.types[0]
	assertRootOrigin := func(captured alterTypePreparation) {
		t.Helper()
		columns := 0
		walkBoundExpression(captured.transformation.expression, func(expression expr.Expr) {
			if column, ok := expression.(columnExpression); ok {
				columns++
				if column.origin != rootColumn || column.slot != 0 || column.ResultUsage().Type.ID() != sqltype.Int4 {
					t.Fatalf("root USING lost original column binding: origin %v want %v, slot %d, usage %v", column.origin, rootColumn, column.slot, column.ResultUsage())
				}
			}
		})
		if columns != 1 || captured.transformation.attribute != parent.Attributes[0] {
			t.Fatal("root capture missing original attribute", columns, captured)
		}
	}
	assertRootOrigin(rootCapture)
	if e.CatalogRead().state != before.state {
		t.Fatal("root capture prematurely published")
	}
	holder.Finish(nil)
	if err := inheritanceAlterJoin(t, result, joined); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(state.types[0], rootCapture) || e.CatalogRead().state != before.state {
		t.Fatal("sole stage replaced capture or published before Finish")
	}
	assertRootOrigin(state.types[0])
	if err := command.scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	for _, pair := range []struct {
		old  *relationDescriptor
		want int64
	}{{parent, 11}, {child, 21}} {
		current := e.CatalogRead().MustRelation(pair.old.address)
		if current.Columns[0].Usage.Type.ID() != sqltype.Int8 || len(e.storage[current.address].versions) != 1 {
			t.Fatal("TYPE capture did not reach physical candidate", current)
		}
		value, err := e.storage[current.address].versions[0].values[0].Integer()
		if err != nil || value != pair.want {
			t.Fatal("descendant did not use its original row", current.name, value, err)
		}
	}
	lockEmpty(t, e)
}
