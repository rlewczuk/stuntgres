package core_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestAlterAddConstraintForeignKeySuccessors(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE p(id INT PRIMARY KEY)")
	sessionSQL(t, s, "INSERT INTO p VALUES(1)")
	sessionSQL(t, s, "CREATE TABLE t(a BIGINT,b INT)")
	sessionSQL(t, s, "INSERT INTO t VALUES(9223372036854775807,1)")
	sessionSQL(t, s, "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID")
	child, parent := alterConstraintDescriptor(t, e, "t"), alterConstraintDescriptor(t, e, "p")
	f := child.Constraints.ForeignKeys[0]
	index := e.Catalog.State.MustRelation(f.Index)
	comparisons, err := table.BindForeignKeyComparison(e.CatalogRead(), child, parent, index, f)
	if err != nil {
		t.Fatal(err)
	}
	value := e.Storage.Relations[child.Address()].Versions[0].Values[0]
	equal, err := comparisons[0].Comparison.EqualChildren(context.Background(), value, value, sqltype.InputEnvironment{})
	if err != nil || !equal {
		t.Fatal("narrowed committed unchanged child key", equal, err)
	}
	tx := transactionBegin(t, s)
	sessionSQL(t, s, "UPDATE t SET b=2")
	scope, err := alterConstraintExecuteScope(t, e, s, "UPDATE t SET b=3")
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	if scope.ProjectedRoots {
		t.Fatal("ordinary UPDATE acquired historical provenance")
	}
	current := scope.Stage.Rows[child.Address()][0]
	rows, err := scope.CaptureForeignKeyRows(e.Catalog.Domain.Ref(e.Catalog.State.Epoch(), child.Address()), ids.ObjectAddress{})
	if err != nil {
		t.Fatal(err)
	}
	previous, ok := rows.Previous(current.Row)
	if !ok || current.Base == 0 || previous.Born != 0 {
		t.Fatal("real prior-own predecessor missing", current, previous)
	}
	err = scope.Finish(nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23503" {
		t.Fatal("prior own key skipped", err)
	}
	scope.Op.Done()
	transactionEnd(t, s, tx, false)
}

func TestAlterAddConstraintProjectionProvenance(t *testing.T) {
	e := engine.New()
	s := sessionStart(t, e, nil)
	for _, q := range []string{"CREATE TABLE p(id INT PRIMARY KEY)", "INSERT INTO p VALUES(1)", "CREATE TABLE t(a INT,b INT)", "CREATE TABLE u(a INT,b INT)"} {
		sessionSQL(t, s, q)
	}
	tx := transactionBegin(t, s)
	for _, q := range []string{"INSERT INTO t VALUES(9,1)", "INSERT INTO u VALUES(9,1)", "ALTER TABLE t ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID", "ALTER TABLE u ADD CONSTRAINT fk FOREIGN KEY(a) REFERENCES p NOT VALID"} {
		sessionSQL(t, s, q)
	}
	tDesc, uDesc := alterConstraintDescriptor(t, e, "t"), alterConstraintDescriptor(t, e, "u")
	// Explicit metadata is private; relation addresses remain the committed refs.
	scope, err := alterConstraintExecuteScope(t, e, s, "ALTER TABLE t ALTER b TYPE BIGINT")
	if err != nil {
		_ = scope.Finish(err)
		t.Fatal(err)
	}
	if !scope.ProjectedRoots || !scope.Stage.Replacing || len(scope.Stage.Rows[tDesc.Address()]) != 1 || len(scope.Stage.Rows[uDesc.Address()]) != 1 {
		t.Fatal("projection not complete historical overlay", scope.Stage.Rows)
	}
	if !reflect.DeepEqual(scope.Stage.Rows[uDesc.Address()], scope.OwnWrites[uDesc.Address()]) {
		t.Fatal("unrelated historical own rows changed")
	}
	if err := scope.Finish(nil); err != nil {
		t.Fatal(err)
	}
	scope.Op.Done()
	write, err := alterConstraintExecuteScope(t, e, s, "UPDATE u SET b=2")
	if err != nil {
		_ = write.Finish(err)
		t.Fatal(err)
	}
	if write.ProjectedRoots {
		t.Fatal("fresh DML got projection exemption")
	}
	err = write.Finish(nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23503" {
		t.Fatal("following born-current DML lost enforcement", err)
	}
	write.Op.Done()
	transactionEnd(t, s, tx, false)
}

func alterConstraintDescriptor(t *testing.T, e *core.Engine, name string) *catalog.Relation {
	t.Helper()
	view := e.CatalogRead()
	return view.MustRelation(enginetest.CatalogRef(t, e, view, name).Address)
}

func alterConstraintExecuteScope(t *testing.T, e *core.Engine, s *session.Session, q string) (*core.StatementScope, error) {
	t.Helper()
	op := alterConstraintOperation(t, s)
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if !scope.Finished {
			_ = scope.Finish(context.Canceled)
		}
	})
	raw, err := parser.Parse(q)
	if err == nil {
		_, err = scope.Execute(raw[0], 0, nil)
	}
	return scope, err
}

func alterConstraintOperation(t *testing.T, s *session.Session) *session.Operation {
	t.Helper()
	op, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(op.Done)
	return op
}
