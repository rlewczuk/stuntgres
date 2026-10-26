package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/core"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

func TestSQLRoutineCallableSelection(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION routine_unit(bigint) RETURNS integer LANGUAGE SQL AS $$SELECT 8$$")
	enginetest.Run(t, e, "CREATE FUNCTION routine_unit(integer) RETURNS integer LANGUAGE SQL AS $$SELECT 4$$")
	sqlRoutineIndexIsolation(t, e)
}

func sqlRoutineIndexIsolation(t *testing.T, e *core.Engine) {
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
	e := engine.New()
	enginetest.Run(t, e, "CREATE SEQUENCE routine_unit_sequence")
	enginetest.Run(t, e, "CREATE FUNCTION routine_unit_next() RETURNS bigint LANGUAGE SQL AS $$SELECT nextval('routine_unit_sequence')$$")
	before := e.CatalogRead().State()
	_, err := enginetest.Execute(e, context.Background(), enginetest.Statement(t, "CREATE FUNCTION routine_unit_bad() RETURNS integer LANGUAGE SQL AS $$SELECT 'x'::text$$"), nil)
	if err == nil || e.CatalogRead().State() != before {
		t.Fatal("invalid CREATE published catalog", err)
	}
	before = e.CatalogRead().State()
	_, err = enginetest.Execute(e, context.Background(), enginetest.Statement(t, "CREATE TABLE routine_unit_unclosed(v float8 DEFAULT ((random())+routine_unit_next()))"), nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || e.CatalogRead().State() != before {
		t.Fatal("SQL default admitted unrelated volatile producer", err)
	}
}

func TestSQLRoutineBodyAnalysisRetention(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE TABLE routine_unit_data(x integer)")
	enginetest.Run(t, e, "INSERT INTO routine_unit_data VALUES(4)")
	enginetest.Run(t, e, "CREATE FUNCTION routine_unit_read() RETURNS integer LANGUAGE SQL AS $$SELECT x FROM routine_unit_data$$")
	op, cleanup, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	execute := func(q string) core.Result {
		t.Helper()
		r, err := enginetest.ExecuteFrame(e, op, enginetest.Statement(t, q), nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	result := execute("SELECT routine_unit_read()")
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	var retained *core.Analysis
	for _, entry := range core.PayloadSQLRoutines(core.PayloadOf(op.Session)) {
		if len(core.RoutineAnalysisDescriptions(&entry)) != 1 {
			t.Fatal("single-command retention shape", len(core.RoutineAnalysisDescriptions(&entry)))
		}
		retained = core.RoutineAnalysisDescriptions(&entry)[0].Analysis
	}
	if retained == nil {
		t.Fatal("body bypassed shared prepared analysis owner")
	}
	execute("UPDATE routine_unit_data SET x=9")
	result = execute("SELECT routine_unit_read()")
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, entry := range core.PayloadSQLRoutines(core.PayloadOf(op.Session)) {
		if len(core.RoutineAnalysisDescriptions(&entry)) != 1 || core.RoutineAnalysisDescriptions(&entry)[0].Analysis != retained {
			t.Fatal("unchanged body identity reanalyzed")
		}
	}
	execute("DISCARD PLANS")
	if len(core.PayloadSQLRoutines(core.PayloadOf(op.Session))) != 0 {
		t.Fatal("DISCARD retained routine body analyses")
	}
	result = execute("SELECT routine_unit_read()")
	if err := result.Rows.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSQLRoutineRestoreStaging(t *testing.T) {
	e := engine.New()
	enginetest.Run(t, e, "CREATE FUNCTION routine_unit_saved(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1+1$$")
	originalView := e.CatalogRead()
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	addresses := originalView.State().ProceduresNamed(namespace, "routine_unit_saved")
	if len(addresses) != 1 {
		t.Fatal("original routine index", addresses)
	}
	originalDefinition := originalView.Get(addresses[0]).(*catalog.Procedure).SQL
	var original bytes.Buffer
	if err := engine.Dump(context.Background(), e, &original); err != nil {
		t.Fatal(err)
	}
	if err := engine.Restore(context.Background(), e, bytes.NewReader(original.Bytes())); err != nil {
		t.Fatal(err)
	}
	if !e.CatalogRead().State().HasDynamicProcedureName("routine_unit_saved") {
		t.Fatal("restore failed to rebuild routine index")
	}
	replacedView := e.CatalogRead()
	replaced := replacedView.State().ProceduresNamed(namespace, "routine_unit_saved")
	if len(replaced) != 1 || originalView.Get(addresses[0]).(*catalog.Procedure).SQL != originalDefinition ||
		replacedView.Get(replaced[0]).(*catalog.Procedure).SQL == originalDefinition {
		t.Fatal("replacement reused staged definition backing or mutated retained catalog")
	}
}

func TestSQLRoutineEmptyBodyAnalysisPublication(t *testing.T) {
	e := engine.New()
	stream := "SET check_function_bodies = false; CREATE FUNCTION routine_unit_kept() RETURNS integer LANGUAGE SQL AS $$SELECT 7$$; CREATE FUNCTION routine_unit_empty() RETURNS integer LANGUAGE SQL AS $$-- no commands$$; CREATE FUNCTION routine_unit_void() RETURNS void LANGUAGE SQL AS $$-- no commands$$;"
	if err := engine.Restore(context.Background(), e, strings.NewReader(stream)); err != nil {
		t.Fatal(err)
	}
	view := e.CatalogRead()
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	address := func(name string) ids.ObjectAddress {
		t.Helper()
		matches := view.State().ProceduresNamed(namespace, name)
		if len(matches) != 1 {
			t.Fatal("routine definition owner", name, matches)
		}
		return matches[0]
	}
	keptAddress, emptyAddress, voidAddress := address("routine_unit_kept"), address("routine_unit_empty"), address("routine_unit_void")
	emptyDefinition := view.Get(emptyAddress).(*catalog.Procedure).SQL
	op, cleanup, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	execute := func(q string) error {
		result, err := enginetest.ExecuteFrame(e, op, enginetest.Statement(t, q), nil)
		if result.Rows != nil {
			if closeErr := result.Rows.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		}
		return err
	}
	if err := execute("SELECT routine_unit_kept()"); err != nil {
		t.Fatal(err)
	}
	accepted := core.PayloadSQLRoutines(core.PayloadOf(op.Session))
	entry, ok := accepted[keptAddress]
	if !ok {
		t.Fatal("valid routine did not publish its analysis owner")
	}
	descriptions := core.RoutineAnalysisDescriptions(&entry)
	if len(descriptions) != 1 || descriptions[0].Analysis == nil {
		t.Fatal("missing accepted command backing")
	}
	backing, analysis := &descriptions[0], descriptions[0].Analysis
	for range 2 {
		if err := execute("SELECT routine_unit_empty()"); err == nil {
			t.Error("empty invocation admitted successful analysis")
		}
		current := core.PayloadSQLRoutines(core.PayloadOf(op.Session))
		if _, ok := current[emptyAddress]; ok || len(current) != 1 {
			t.Fatal("failed invocation published retained analysis")
		}
		incumbent := current[keptAddress]
		retained := core.RoutineAnalysisDescriptions(&incumbent)
		if len(retained) != 1 || &retained[0] != backing || retained[0].Analysis != analysis {
			t.Fatal("rejected publication changed incumbent analysis backing")
		}
		if e.CatalogRead().Get(emptyAddress).(*catalog.Procedure).SQL != emptyDefinition || view.Get(emptyAddress).(*catalog.Procedure).SQL != emptyDefinition {
			t.Fatal("failed analysis changed immutable declaration ownership")
		}
	}
	if err := execute("SELECT routine_unit_void()"); err != nil {
		t.Fatal(err)
	}
	current := core.PayloadSQLRoutines(core.PayloadOf(op.Session))
	voidEntry, ok := current[voidAddress]
	if !ok || len(core.RoutineAnalysisDescriptions(&voidEntry)) != 0 || len(current) != 2 {
		t.Fatal("valid empty VOID analysis was not retained independently")
	}
	incumbent := current[keptAddress]
	retained := core.RoutineAnalysisDescriptions(&incumbent)
	if len(retained) != 1 || &retained[0] != backing || retained[0].Analysis != analysis {
		t.Fatal("empty VOID publication changed incumbent backing")
	}
}
