package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceFDWNativeControls(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		run(fixture.Select("SELECT id FROM fdw_data ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		if b.PG {
			return
		}
		q := "SELECT id,label FROM fdw_data ORDER BY id"
		held := fdwHeld(t, p, b, c, q)
		before := r.snapshot()
		var dump bytes.Buffer
		dump.WriteString("sentinel")
		fixture.TransactionOrdinary(t, stuntgres.Dump(p.Ctx, p.Native, &dump), "stuntgres: dump cannot serialize SQL/MED foreign metadata")
		if dump.String() != "sentinel" || r.snapshot() != before {
			t.Fatal("Dump wrote bytes or invoked provider", dump.String(), before, r.snapshot())
		}
		foreignRestore := func() {
			t.Helper()
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE must_not_publish(v integer); CREATE FOREIGN DATA WRAPPER forbidden"))
			fixture.AssertControlDiagnostic(t, err, fdwDiagnostic("0A000", "restore cannot replay SQL/MED foreign metadata"))
		}
		foreignRestore()
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='must_not_publish'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if r.snapshot() != before {
			t.Fatal("failed replay invoked provider")
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DECLARE fdw_old NO SCROLL CURSOR FOR SELECT id,label FROM fdw_data", 0))
		first := fdwHeld(t, p, b, c, "FETCH 1 FROM fdw_old")
		live := r.snapshot()
		foreignRestore()
		run(fixture.Select("FETCH 1 FROM fdw_old", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(2), nil}}))
		if after := r.snapshot(); after.begins != live.begins || after.ends != live.ends || after.generations != live.generations {
			t.Fatal("failed Restore replaced live source", live, after)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		stale := r.snapshot()
		if stale.openPools != 1 || stale.openCursors != 1 {
			t.Fatal("replacement changed deferred cursor ownership", stale)
		}
		cleanupFailure := errors.New("stale cursor cleanup failed")
		r.setHook(func(ctx context.Context, method string) error {
			if method == "EndForeignScan" {
				return cleanupFailure
			}
			return ctx.Err()
		})
		rows, err := c.QueryContext(p.Ctx, "FETCH ALL FROM fdw_old")
		if rows != nil {
			rows.Close()
			t.Fatal("stale cursor published rows")
		}
		fixture.TransactionOrdinary(t, err, "stuntgres: catalog replaced before publication")
		r.setHook(nil)
		after := fdwReleased(t, r, before, 1)
		if after.iterations != stale.iterations {
			t.Fatal("stale request pulled provider before epoch failure", stale, after)
		}
		run(fixture.Exec("ROLLBACK", 0))
		fixture.RunStep(t, p.Ctx, first, first.recorder, fixture.Select(first.query, []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
		fixture.RunStep(t, p.Ctx, held, held.recorder, fixture.Select(q, []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}))
		run(fixture.Select("SELECT count(*) FROM pg_foreign_table", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='fdw_sql_handler'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		fdwInstallLive(b, run)
		retained := fdwHeld(t, p, b, c, q)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE restored(v integer); INSERT INTO restored VALUES(99)")); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, retained, retained.recorder, fixture.Select(q, []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}))
		run(fixture.Select("SELECT v FROM restored", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		fdwInstallLive(b, run)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE canceled(v integer)")); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		run(fixture.Select("SELECT id FROM fdw_data ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		fdwConfiguredNamespaceControls(t, p.Ctx, r)
	})
}

func fdwConfiguredNamespaceControls(t *testing.T, ctx context.Context, r *fdwSQLReader) {
	t.Helper()
	before := r.snapshot()
	recorder := &fixture.Recorder{RawBackend: fixture.RawNative}
	configured := func() *stuntgres.Driver {
		d := stuntgres.NewDriver()
		if err := d.RegisterForeignDataWrapper(stuntgres.ForeignDataWrapperDefinition{HandlerName: stuntgres.ForeignFunctionName{Schema: "fdw_cap", Name: "handler"}, Wrapper: r}); err != nil {
			t.Fatal(err)
		}
		return d
	}
	pool := func(d *stuntgres.Driver, name string) (*sql.DB, *sql.DB) {
		connector, err := d.OpenConnector(name)
		if err != nil {
			t.Fatal(err)
		}
		db := fdwRecordedPool(t, connector)
		native := sql.OpenDB(connector)
		t.Cleanup(func() {
			db.Close()
			native.Close()
		})
		return db, native
	}
	source, sourceControl := pool(configured(), "fdw-namespace-source")
	for _, step := range []fixture.Step{fixture.Exec("CREATE ROLE fdw_cap_owner", 0), fixture.Exec("CREATE TABLE fdw_cap.ordinary(v integer)", 0), fixture.Exec("INSERT INTO fdw_cap.ordinary VALUES(7),(8)", 2), fixture.Exec("COMMENT ON SCHEMA fdw_cap IS 'registered namespace'", 0), fixture.Exec("ALTER SCHEMA fdw_cap OWNER TO fdw_cap_owner", 0)} {
		fixture.RunStep(t, ctx, source, recorder, step)
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(ctx, sourceControl, &dump); err != nil {
		t.Fatal(err)
	}
	var matchingTarget, matchingControl, unconfiguredTarget *sql.DB
	for _, matching := range []bool{true, false} {
		d := stuntgres.NewDriver()
		if matching {
			d = configured()
		}
		target, targetControl := pool(d, "fdw-namespace-target")
		if matching {
			matchingTarget = target
			matchingControl = targetControl
		} else {
			unconfiguredTarget = target
		}
		fixture.RunStep(t, ctx, target, recorder, fixture.Exec("CREATE ROLE fdw_cap_owner", 0))
		if err := stuntgres.Restore(ctx, targetControl, strings.NewReader(dump.String())); err != nil {
			t.Fatal("matching registry", matching, err)
		}
		fixture.RunStep(t, ctx, target, recorder, fixture.Select("SELECT v FROM fdw_cap.ordinary ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
		fixture.RunStep(t, ctx, target, recorder, fixture.Select("SELECT n.nspname,r.rolname,obj_description(n.oid,'pg_namespace') AS comment FROM pg_namespace n JOIN pg_roles r ON r.oid=n.nspowner WHERE n.nspname='fdw_cap'", []string{"nspname", "rolname", "comment"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{"fdw_cap", "fdw_cap_owner", "registered namespace"}}))
	}
	for _, step := range []fixture.Step{fixture.Exec("CREATE SCHEMA fdw_unrelated", 0), fixture.Exec("CREATE TABLE fdw_unrelated.kept(v integer)", 0), fixture.Exec("INSERT INTO fdw_unrelated.kept VALUES(8)", 1), fixture.Exec("ALTER SCHEMA fdw_unrelated RENAME TO fdw_unrelated_renamed", 0)} {
		fixture.RunStep(t, ctx, source, recorder, step)
	}
	var unrelated bytes.Buffer
	if err := stuntgres.Dump(ctx, sourceControl, &unrelated); err != nil {
		t.Fatal("unrelated rename was mistaken for registered namespace retirement", err)
	}
	roundtrip, roundtripControl := pool(configured(), "fdw-unrelated-rename-target")
	fixture.RunStep(t, ctx, roundtrip, recorder, fixture.Exec("CREATE ROLE fdw_cap_owner", 0))
	if err := stuntgres.Restore(ctx, roundtripControl, strings.NewReader(unrelated.String())); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, ctx, roundtrip, recorder, fixture.Select("SELECT v FROM fdw_unrelated_renamed.kept", []string{"v"}, []string{"INT4"}, [][]any{{int64(8)}}))
	fixture.RunStep(t, ctx, source, recorder, fixture.Exec("ALTER SCHEMA fdw_cap RENAME TO fdw_retired_cap", 0))
	rejected := func() {
		t.Helper()
		var dst bytes.Buffer
		dst.WriteString("sentinel")
		fixture.TransactionOrdinary(t, stuntgres.Dump(ctx, sourceControl, &dst), "stuntgres: dump cannot serialize a renamed or retired configured foreign namespace")
		if dst.String() != "sentinel" {
			t.Fatal("failed namespace preflight wrote bytes")
		}
	}
	rejected()
	fixture.RunStep(t, ctx, source, recorder, fixture.Exec("DROP SCHEMA fdw_retired_cap CASCADE", 0))
	fixture.RunStep(t, ctx, source, recorder, fixture.Exec("CREATE SCHEMA fdw_cap", 0))
	rejected()
	if after := r.snapshot(); after != before {
		t.Fatal("ordinary namespace control invoked foreign callbacks", before, after)
	}
	fixture.RunStep(t, ctx, unconfiguredTarget, recorder, fixture.ErrorDiagnostic("CREATE FOREIGN DATA WRAPPER fdw_cap_reader HANDLER fdw_cap.handler", fdwDiagnostic("42883", "function fdw_cap.handler() does not exist")))
	createWrapper := fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_cap_reader HANDLER fdw_cap.handler", 0)
	wrapperStatement, err := matchingTarget.PrepareContext(ctx, createWrapper.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer wrapperStatement.Close()
	preparedWrapper := fixture.HeldStatement{Executor: matchingTarget, Statement: wrapperStatement, SQL: createWrapper.Query}
	fixture.RunStep(t, ctx, preparedWrapper, recorder, createWrapper)
	for _, step := range []fixture.Step{fixture.Exec("CREATE SERVER fdw_cap_server FOREIGN DATA WRAPPER fdw_cap_reader", 0), fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_cap_server", 0), fixture.Exec("CREATE FOREIGN TABLE fdw_cap.remote_data(id integer,label text,flag boolean) SERVER fdw_cap_server OPTIONS(schema_name 'fdw_source',table_name 'data')", 0)} {
		fixture.RunStep(t, ctx, matchingTarget, recorder, step)
	}
	before = r.snapshot()
	fixture.RunStep(t, ctx, matchingTarget, recorder, fdwRows("SELECT id,label,flag FROM fdw_cap.remote_data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}))
	fdwReleased(t, r, before, 1)
	// The registered procedure is a real namespace child. Its wrapper,
	// server and mapping live outside that schema; move the table outside as
	// well so CASCADE must follow the handler's actual dependency graph.
	fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Exec("ALTER FOREIGN TABLE fdw_cap.remote_data SET SCHEMA public", 0))
	fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Exec("CREATE VIEW public.fdw_cap_view AS SELECT id,label,flag FROM public.remote_data", 0))
	query := fdwRows("SELECT id,label,flag FROM public.remote_data ORDER BY id", [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}})
	statement, err := matchingTarget.PrepareContext(ctx, query.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer statement.Close()
	preparedRows := fixture.HeldStatement{Executor: matchingTarget, Statement: statement, SQL: query.Query}
	before = r.snapshot()
	fixture.RunStep(t, ctx, preparedRows, recorder, query)
	fdwReleased(t, r, before, 1)
	var rowType, arrayType int64
	if err := matchingTarget.QueryRowContext(ctx, "SELECT 'public.remote_data'::regtype::oid,'public.remote_data[]'::regtype::oid").Scan(&rowType, &arrayType); err != nil {
		t.Fatal(err)
	}
	if rowType == 0 || arrayType == 0 || rowType == arrayType {
		t.Fatal("foreign row/array type identities", rowType, arrayType)
	}
	typeEdges := func() {
		t.Helper()
		fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1247::oid AND objid=$1::oid AND refclassid=1259::oid AND refobjid='public.remote_data'::regclass::oid", []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}, rowType))
		fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Select("SELECT deptype FROM pg_depend WHERE classid=1247::oid AND objid=$1::oid AND refclassid=1247::oid AND refobjid=$2::oid", []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}, arrayType, rowType))
	}
	typeEdges()
	identities := []struct {
		table, predicate string
		oid              int64
	}{
		{table: "pg_namespace", predicate: "nspname='fdw_cap'"},
		{table: "pg_proc", predicate: "proname='handler'"},
		{table: "pg_foreign_data_wrapper", predicate: "fdwname='fdw_cap_reader'"},
		{table: "pg_foreign_server", predicate: "srvname='fdw_cap_server'"},
		{table: "pg_user_mapping", predicate: "umuser='postgres'::regrole::oid"},
		{table: "pg_class", predicate: "relname='remote_data'"},
		{table: "pg_class", predicate: "relname='fdw_cap_view'"},
	}
	for i := range identities {
		item := &identities[i]
		if err := matchingTarget.QueryRowContext(ctx, "SELECT oid FROM "+item.table+" WHERE "+item.predicate).Scan(&item.oid); err != nil {
			t.Fatal(item.table, err)
		}
	}
	restrict := fdwDiagnostic("2BP01", "cannot drop schema fdw_cap because other objects depend on it")
	restrict.Detail = strings.Join([]string{
		"function fdw_cap.handler() depends on schema fdw_cap",
		"foreign-data wrapper fdw_cap_reader depends on function fdw_cap.handler()",
		"server fdw_cap_server depends on foreign-data wrapper fdw_cap_reader",
		"user mapping for postgres on server fdw_cap_server depends on server fdw_cap_server",
		"foreign table remote_data depends on server fdw_cap_server",
		"view fdw_cap_view depends on foreign table remote_data",
		"table fdw_cap.ordinary depends on schema fdw_cap",
	}, "\n")
	restrict.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
	before = r.snapshot()
	fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.ErrorDiagnostic("DROP SCHEMA fdw_cap RESTRICT", restrict))
	if after := r.snapshot(); after != before {
		t.Fatal("namespace RESTRICT entered foreign callbacks", before, after)
	}
	typeEdges()
	for _, item := range identities {
		fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Select("SELECT oid=$1::oid AS retained FROM "+item.table+" WHERE "+item.predicate, []string{"retained"}, []string{"BOOL"}, [][]any{{true}}, item.oid))
	}
	viewQuery := query
	viewQuery.Query = "SELECT id,label,flag FROM public.fdw_cap_view ORDER BY id"
	before = r.snapshot()
	fixture.RunStep(t, ctx, matchingTarget, recorder, viewQuery)
	fdwReleased(t, r, before, 1)
	fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Select("SELECT v FROM fdw_cap.ordinary ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}}))
	heldRecorder := &fixture.Recorder{RawBackend: fixture.RawNative}
	before = r.snapshot()
	heldRows, err := matchingTarget.QueryContext(context.WithValue(ctx, fixture.RecorderKey{}, heldRecorder), query.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer heldRows.Close()
	finalized := fdwHeldResult{Executor: matchingTarget, query: query.Query, rows: heldRows, recorder: heldRecorder}
	fdwReleased(t, r, before, 1)
	before = r.snapshot()
	fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Exec("DROP SCHEMA fdw_cap CASCADE", 0))
	for _, query := range []string{
		"SELECT count(*) FROM pg_namespace WHERE nspname='fdw_cap'",
		"SELECT count(*) FROM pg_proc WHERE proname='handler'",
		"SELECT count(*) FROM pg_foreign_data_wrapper",
		"SELECT count(*) FROM pg_foreign_server",
		"SELECT count(*) FROM pg_user_mapping",
		"SELECT count(*) FROM pg_foreign_table",
		"SELECT count(*) FROM pg_class WHERE relname='remote_data'",
		"SELECT count(*) FROM pg_class WHERE relname='fdw_cap_view'",
	} {
		fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Select(query, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
	if after := r.snapshot(); after != before {
		t.Fatal("namespace cascade entered foreign callbacks", before, after)
	}
	for _, oid := range []int64{rowType, arrayType} {
		fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE (classid=1247::oid AND objid=$1::oid) OR (refclassid=1247::oid AND refobjid=$1::oid)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, oid))
	}
	for _, item := range identities {
		class := "'" + item.table + "'::regclass::oid"
		fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Select("SELECT count(*) FROM pg_depend WHERE (classid="+class+" AND objid=$1::oid) OR (refclassid="+class+" AND refobjid=$1::oid)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, item.oid))
	}
	for _, name := range []string{"public.remote_data", "public.remote_data[]"} {
		absent := fdwDiagnostic("42704", `type "public.remote_data" does not exist`)
		absent.Position = "14"
		step := fixture.ErrorDiagnostic("SELECT NULL::"+name, absent)
		step.SelectRows = true
		fixture.RunStep(t, ctx, matchingTarget, recorder, step)
	}
	fixture.RunStep(t, ctx, finalized, finalized.recorder, query)
	if after := r.snapshot(); after != before {
		t.Fatal("finalized namespace rows performed provider work", before, after)
	}
	missingRelation := fdwDiagnostic("42P01", `relation "public.remote_data" does not exist`)
	missingRelation.Position = "27"
	missingRows := fixture.ErrorDiagnostic(query.Query, missingRelation)
	missingRows.SelectRows = true
	for _, phase := range []struct {
		name      string
		recreated bool
	}{
		{name: "dropped_namespace"},
		{name: "recreated_namespace_without_callable", recreated: true},
	} {
		if !t.Run(phase.name, func(t *testing.T) {
			handlerDiagnostic := fdwDiagnostic("42883", "function fdw_cap.handler() does not exist")
			if phase.recreated {
				fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Exec("CREATE SCHEMA fdw_cap", 0))
			} else {
				handlerDiagnostic = fdwDiagnostic("3F000", `schema "fdw_cap" does not exist`)
				handlerDiagnostic.Position = "52"
			}
			fixture.RunStep(t, ctx, preparedRows, recorder, missingRows)
			fixture.RunStep(t, ctx, preparedWrapper, recorder, fixture.ErrorDiagnostic(createWrapper.Query, handlerDiagnostic))
			fixture.RunStep(t, ctx, matchingTarget, recorder, fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='handler'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			if after := r.snapshot(); after != before {
				t.Fatal("dropped prepared identity invoked provider", before, after)
			}
		}) {
			t.FailNow()
		}
	}
	if !t.Run("genuine_reset_reseeds_registration", func(t *testing.T) {
		sourceRows, err := r.source.QueryContext(ctx, "SELECT id,label,flag FROM fdw_source.data ORDER BY id")
		fixture.DeleteReferenceConsume(t, false, sourceRows, err, query)
		if err := stuntgres.Reset(ctx, matchingControl); err != nil {
			t.Fatal(err)
		}
		if after := r.snapshot(); after != before {
			t.Fatal("registration reseed invoked provider", before, after)
		}
		fixture.RunStep(t, ctx, preparedWrapper, recorder, createWrapper)
		for _, step := range []fixture.Step{
			fixture.Exec("CREATE SERVER fdw_cap_server FOREIGN DATA WRAPPER fdw_cap_reader", 0),
			fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_cap_server", 0),
			fixture.Exec("CREATE FOREIGN TABLE public.remote_data(id integer,label text,flag boolean) SERVER fdw_cap_server OPTIONS(schema_name 'fdw_source',table_name 'data')", 0),
		} {
			fixture.RunStep(t, ctx, matchingTarget, recorder, step)
		}
		before = r.snapshot()
		fixture.RunStep(t, ctx, preparedRows, recorder, query)
		fdwReleased(t, r, before, 1)
	}) {
		t.FailNow()
	}
}
