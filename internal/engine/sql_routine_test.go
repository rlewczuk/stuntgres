package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func sqlRoutineUnitInteger(t *testing.T, result Result, want int64) {
	t.Helper()
	if result.Rows == nil {
		t.Fatal("missing routine rows")
	}
	defer result.Rows.Close()
	row := make([]sqltype.Value, 1)
	if ok, err := result.Rows.Next(context.Background(), row); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if got, err := row[0].Integer(); err != nil || got != want {
		t.Fatal(got, err, want)
	}
	if ok, err := result.Rows.Next(context.Background(), row); ok || err != nil {
		t.Fatal("extra routine result", ok, err)
	}
}

func TestSQLRoutineCallableSelection(t *testing.T) {
	e := New()
	run(t, e, "CREATE FUNCTION routine_unit(bigint) RETURNS integer LANGUAGE SQL AS $$SELECT 8$$")
	run(t, e, "CREATE FUNCTION routine_unit(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 4$$")
	sqlRoutineUnitInteger(t, run(t, e, "SELECT routine_unit(1)"), 4)
	sqlRoutineUnitInteger(t, run(t, e, "SELECT routine_unit(1::bigint)"), 8)
	sqlRoutineIndexIsolation(t, e)
}

func sqlRoutineIndexIsolation(t *testing.T, e *Engine) {
	t.Helper()
	view := e.CatalogRead()
	key := catalog.ProcedureNameKey{Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "routine_unit"}
	addresses := view.State().ProceduresNamed(key.Namespace, key.Name)
	if len(addresses) != 2 || !view.State().HasSQLProcedureName(key.Name) {
		t.Fatal("routine index missing overloads")
	}
	old := view.Get(addresses[0]).(*catalog.Procedure)
	// Spare capacity must never let a touched COW bucket overwrite its parent.
	index := catalog.RebuildProcedureNames(view.Descriptors())
	bucket := make([]ids.ObjectAddress, 2, 4)
	copy(bucket, addresses)
	index.Buckets[key] = bucket
	metadata := *old
	metadata.ObjectRevision++
	edit := catalog.ProcedureIndexEdit{Index: index}
	edit.Replace(old, &metadata)
	if edit.Index != index {
		t.Fatal("metadata replacement copied name membership")
	}
	renamed := *old
	renamed.ObjectName = "routine_renamed"
	edit.Replace(old, &renamed)
	if len(index.Buckets[key]) != 2 || len(edit.Index.Buckets[key]) != 1 || edit.Index.SQLNames[renamed.Name()] != 1 {
		t.Fatal("routine rename corrupted catalog index")
	}
	edit.Replace(&renamed, nil)
	if edit.Index.SQLNames[renamed.Name()] != 0 {
		t.Fatal("removed SQL name gate retained")
	}
	stage := e.BeginCatalogStage(view)
	defer e.DiscardCatalogStage(stage)
	if err := stage.Replace(&metadata); err != nil {
		t.Fatal(err)
	}
	candidate := stage.State()
	if len(candidate.ProceduresNamed(key.Namespace, key.Name)) != 2 || candidate.Get(old.Address()) != &metadata || view.Get(old.Address()) != old {
		t.Fatal("candidate metadata overlay lost root isolation")
	}
	reset := catalog.CloneState(view.State())
	reset.ResetMaps(0)
	if reset.HasSQLProcedureName(key.Name) || len(reset.ProceduresNamed(key.Namespace, key.Name)) != 0 {
		t.Fatal("reset retained routine name index")
	}
}

func TestSQLRoutineFirstRowDemand(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE routine_unit_rows(x integer)")
	run(t, e, "INSERT INTO routine_unit_rows VALUES (0),(1)")
	run(t, e, "CREATE FUNCTION routine_unit_first() RETURNS integer LANGUAGE SQL AS $$SELECT 10/x FROM (SELECT x FROM routine_unit_rows ORDER BY x DESC) q$$")
	sqlRoutineUnitInteger(t, run(t, e, "SELECT routine_unit_first()"), 10)
}

func TestSQLRoutineValidationNoExecution(t *testing.T) {
	e := New()
	run(t, e, "CREATE SEQUENCE routine_unit_sequence")
	run(t, e, "CREATE FUNCTION routine_unit_next() RETURNS bigint LANGUAGE SQL AS $$SELECT nextval('routine_unit_sequence')$$")
	run(t, e, "CREATE FUNCTION routine_unit_division() RETURNS integer LANGUAGE SQL AS $$SELECT 1/0$$")
	sqlRoutineUnitInteger(t, run(t, e, "SELECT routine_unit_next()"), 1)
	run(t, e, "CREATE TABLE routine_unit_defaults(v bigint DEFAULT routine_unit_next()+1)")
	run(t, e, "CREATE TABLE routine_unit_error_default(v integer DEFAULT routine_unit_division())")
	sqlRoutineUnitInteger(t, run(t, e, "SELECT routine_unit_next()"), 2)
	run(t, e, "INSERT INTO routine_unit_defaults DEFAULT VALUES")
	sqlRoutineUnitInteger(t, run(t, e, "SELECT v FROM routine_unit_defaults"), 4)
	before := e.CatalogRead().State()
	_, err := testExecute(e, context.Background(), statement(t, "CREATE FUNCTION routine_unit_bad() RETURNS integer LANGUAGE SQL AS $$SELECT 'x'::text$$"), nil)
	if err == nil || e.CatalogRead().State() != before {
		t.Fatal("invalid CREATE published catalog", err)
	}
	before = e.CatalogRead().State()
	_, err = testExecute(e, context.Background(), statement(t, "CREATE TABLE routine_unit_unclosed(v float8 DEFAULT ((random())+routine_unit_next()))"), nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || e.CatalogRead().State() != before {
		t.Fatal("SQL default admitted unrelated volatile producer", err)
	}
}

func TestSQLRoutineBodyAnalysisRetention(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE routine_unit_data(x integer)")
	run(t, e, "INSERT INTO routine_unit_data VALUES(4)")
	run(t, e, "CREATE FUNCTION routine_unit_read() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM routine_unit_data$$")
	op, cleanup, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	execute := func(q string) Result {
		t.Helper()
		r, err := testExecuteFrame(e, op, statement(t, q), nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	sqlRoutineUnitInteger(t, execute("SELECT routine_unit_read()"), 4)
	var retained *preparedAnalysis
	for _, entry := range payloadOf(op.Session).sqlRoutines {
		if len(entry.descriptions) != 1 {
			t.Fatal("single-command retention shape", len(entry.descriptions))
		}
		retained = entry.descriptions[0].analysis
	}
	if retained == nil {
		t.Fatal("body bypassed shared prepared analysis owner")
	}
	execute("UPDATE routine_unit_data SET x=9")
	sqlRoutineUnitInteger(t, execute("SELECT routine_unit_read()"), 9)
	for _, entry := range payloadOf(op.Session).sqlRoutines {
		if len(entry.descriptions) != 1 || entry.descriptions[0].analysis != retained {
			t.Fatal("unchanged body identity reanalyzed")
		}
	}
	execute("DISCARD PLANS")
	if len(payloadOf(op.Session).sqlRoutines) != 0 {
		t.Fatal("DISCARD retained routine body analyses")
	}
	sqlRoutineUnitInteger(t, execute("SELECT routine_unit_read()"), 9)
}

func TestSQLRoutineRestoreStaging(t *testing.T) {
	e := New()
	run(t, e, "CREATE FUNCTION routine_unit_saved(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$")
	var original bytes.Buffer
	if err := e.Dump(context.Background(), &original); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), strings.NewReader(routine.RestoreDirective+" CREATE FUNCTION invalid_sql() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM missing_table$$;")); err == nil {
		t.Fatal("invalid deferred body published")
	}
	var after bytes.Buffer
	if err := e.Dump(context.Background(), &after); err != nil || !bytes.Equal(original.Bytes(), after.Bytes()) {
		t.Fatal("failed stage changed target", err)
	}
	if err := e.Restore(context.Background(), bytes.NewReader(original.Bytes())); err != nil {
		t.Fatal(err)
	}
	sqlRoutineUnitInteger(t, run(t, e, "SELECT routine_unit_saved(4)"), 5)
	if !e.CatalogRead().State().HasSQLProcedureName("routine_unit_saved") {
		t.Fatal("restore failed to rebuild routine index")
	}
}
