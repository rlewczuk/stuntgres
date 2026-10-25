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
	"testing"
)

func TestSQLRoutineCallableSelection(t *testing.T) {
	e := New()
	run(t, e, "CREATE FUNCTION routine_unit(bigint) RETURNS integer LANGUAGE SQL AS $$SELECT 8$$")
	run(t, e, "CREATE FUNCTION routine_unit(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 4$$")
	sqlRoutineIndexIsolation(t, e)
}

func sqlRoutineIndexIsolation(t *testing.T, e *Engine) {
	t.Helper()
	view := e.CatalogRead()
	key := catalog.ProcedureNameKey{Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "routine_unit"}
	addresses := view.State().ProceduresNamed(key.Namespace, key.Name)
	if len(addresses) != 2 || !view.State().HasDynamicProcedureName(key.Name) {
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
	if len(index.Buckets[key]) != 2 || len(edit.Index.Buckets[key]) != 1 || edit.Index.DynamicNames[renamed.Name()] != 1 {
		t.Fatal("routine rename corrupted catalog index")
	}
	edit.Replace(&renamed, nil)
	if edit.Index.DynamicNames[renamed.Name()] != 0 {
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
	reset.ResetMaps()
	if reset.HasDynamicProcedureName(key.Name) || len(reset.ProceduresNamed(key.Namespace, key.Name)) != 0 {
		t.Fatal("reset retained routine name index")
	}
}

func TestSQLRoutineValidationNoExecution(t *testing.T) {
	e := New()
	run(t, e, "CREATE SEQUENCE routine_unit_sequence")
	run(t, e, "CREATE FUNCTION routine_unit_next() RETURNS bigint LANGUAGE SQL AS $$SELECT nextval('routine_unit_sequence')$$")
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
	result := execute("SELECT routine_unit_read()")
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	var retained *PreparedAnalysis
	for _, entry := range PayloadSQLRoutines(PayloadOf(op.Session)) {
		if len(RoutineAnalysisDescriptions(&entry)) != 1 {
			t.Fatal("single-command retention shape", len(RoutineAnalysisDescriptions(&entry)))
		}
		retained = DescriptionAnalysis(&RoutineAnalysisDescriptions(&entry)[0])
	}
	if retained == nil {
		t.Fatal("body bypassed shared prepared analysis owner")
	}
	execute("UPDATE routine_unit_data SET x=9")
	result = execute("SELECT routine_unit_read()")
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range PayloadSQLRoutines(PayloadOf(op.Session)) {
		if len(RoutineAnalysisDescriptions(&entry)) != 1 || DescriptionAnalysis(&RoutineAnalysisDescriptions(&entry)[0]) != retained {
			t.Fatal("unchanged body identity reanalyzed")
		}
	}
	execute("DISCARD PLANS")
	if len(PayloadSQLRoutines(PayloadOf(op.Session))) != 0 {
		t.Fatal("DISCARD retained routine body analyses")
	}
	result = execute("SELECT routine_unit_read()")
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLRoutineRestoreStaging(t *testing.T) {
	e := New()
	run(t, e, "CREATE FUNCTION routine_unit_saved(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$")
	var original bytes.Buffer
	if err := Dump(context.Background(), e, &original); err != nil {
		t.Fatal(err)
	}
	if err := Restore(context.Background(), e, strings.NewReader(routine.RestoreDirective+" CREATE FUNCTION invalid_sql() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM missing_table$$;")); err == nil {
		t.Fatal("invalid deferred body published")
	}
	var after bytes.Buffer
	if err := Dump(context.Background(), e, &after); err != nil || !bytes.Equal(original.Bytes(), after.Bytes()) {
		t.Fatal("failed stage changed target", err)
	}
	if err := Restore(context.Background(), e, bytes.NewReader(original.Bytes())); err != nil {
		t.Fatal(err)
	}
	if !e.CatalogRead().State().HasDynamicProcedureName("routine_unit_saved") {
		t.Fatal("restore failed to rebuild routine index")
	}
}
