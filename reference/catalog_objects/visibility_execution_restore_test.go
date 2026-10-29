package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func executionVisibilityStored(run func(fixture.Step), create bool) {
	if create {
		var calls []string
		for i, fn := range executionVisibilityNames {
			suffix := []string{"f", "o", "c", "m"}[i]
			calls = append(calls, fn+"(NULL) AS n"+suffix, fn+"(NULL::integer) AS w"+suffix)
		}
		run(fixture.Exec("CREATE VIEW public.vex_nulls AS SELECT "+strings.Join(calls, ","), 0))
	}
	nulls := fixture.Select("SELECT * FROM public.vex_nulls", []string{"nf", "wf", "no", "wo", "nc", "wc", "nm", "wm"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{nil, nil, nil, nil, nil, nil, nil, nil}})
	run(nulls)
	run(fixture.Prepared(nulls))
	for kind, object := range executionVisibilityObjects()[:4] {
		name := "public.vex_stored_"+[]string{"function", "operator", "class", "family"}[kind]
		if create {
			// Discover identities at execution, never store allocator-dependent OID constants.
			source := []string{
				"pg_proc c JOIN pg_namespace n ON n.oid=c.pronamespace WHERE n.nspname='vex_a' AND c.proname='vis_fn' AND c.proargtypes::text='23'",
				"pg_operator c JOIN pg_namespace n ON n.oid=c.oprnamespace WHERE n.nspname='vex_a' AND c.oprname='##' AND c.oprleft=23 AND c.oprright=23",
				"pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace WHERE n.nspname='vex_a' AND c.opcname='vis_class' AND c.opcmethod=403",
				"pg_opfamily c JOIN pg_namespace n ON n.oid=c.opfnamespace WHERE n.nspname='vex_a' AND c.opfname='vis_family' AND c.opfmethod=403",
			}[kind]
			run(fixture.Exec("CREATE VIEW "+name+" AS SELECT pg_catalog."+object.fn+"(c.oid) AS visible FROM "+source, 0))
		}
	}
	for _, path := range []string{"vex_a,vex_b", "vex_b,vex_a", "public"} {
		run(fixture.Exec("SET search_path="+path, 0))
		for _, name := range []string{"function", "operator", "class", "family"} {
			s := fixture.Select("SELECT visible FROM public.vex_stored_"+name, []string{"visible"}, []string{"BOOL"}, [][]any{{path == "vex_a,vex_b"}})
			run(s); run(fixture.Prepared(s))
		}
		for _, routine := range []string{"vis_proc(integer)", "vis_agg(integer)"} {
			run(executionVisibilityDecision(executionVisibilityNames[0], "'vex_a."+routine+"'::regprocedure", path == "vex_a,vex_b"))
			run(executionVisibilityDecision(executionVisibilityNames[0], "'vex_b."+routine+"'::regprocedure", path == "vex_b,vex_a"))
		}
	}
}

func executionVisibilityRoundtrip(t *testing.T, p *fixture.Pair, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	if b.PG { return }
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil { t.Fatal(err) }
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil { t.Fatal(err) }
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	executionVisibilityOrders(run, 0, 1, 2, 3)
	executionVisibilityStored(run, false)
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+" SELECT vex_missing_restore_column;")); err == nil { t.Fatal("invalid restore published") }
	executionVisibilityOrders(run, 0, 1, 2, 3)
	executionVisibilityStored(run, false)
	connector, err := stuntgres.NewDriver().OpenConnector(t.Name()+"-independent")
	if err != nil { t.Fatal(err) }
	target := sql.OpenDB(connector)
	defer target.Close()
	if err := stuntgres.Restore(p.Ctx, target, bytes.NewReader(dump.Bytes())); err != nil { t.Fatal(err) }
	conn, err := target.Conn(p.Ctx)
	if err != nil { t.Fatal(err) }
	defer conn.Close()
	independent := func(s fixture.Step) {
		if !s.SelectRows {
			result, err := conn.ExecContext(p.Ctx, s.Query, s.Args...)
			if err != nil { t.Fatal(err) }
			if count, err := result.RowsAffected(); err != nil || count != 0 { t.Fatal(count, err) }
			return
		}
		var rows *sql.Rows
		var err error
		if s.Prepared {
			stmt, prepareErr := conn.PrepareContext(p.Ctx, s.Query)
			if prepareErr != nil { t.Fatal(prepareErr) }
			defer stmt.Close()
			rows, err = stmt.QueryContext(p.Ctx, s.Args...)
		} else {
			rows, err = conn.QueryContext(p.Ctx, s.Query, s.Args...)
		}
		if err != nil { t.Fatal(err) }
		fixture.VisibilityBoolMetadata(t, rows)
		fixture.DeleteReferenceConsume(t, false, rows, err, s)
	}
	executionVisibilityOrders(independent, 0, 1, 2, 3)
	executionVisibilityStored(independent, false)
	if err := stuntgres.Restore(p.Ctx, target, strings.NewReader("SELECT vex_missing_restore_column;")); err == nil { t.Fatal("invalid independent restore published") }
	executionVisibilityOrders(independent, 0, 1, 2, 3)
}

func TestReferenceVisibilityExecutionStoredRestore(t *testing.T) {
	executionVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		executionVisibilityStored(run, true)
		executionVisibilityHeldViews(t, p, b, conn, run)
		executionVisibilityRoundtrip(t, p, b, run)
		executionVisibilityHeldViews(t, p, b, conn, run)
	})
}

func TestReferenceVisibilityExecutionFeatureSmoke(t *testing.T) {
	executionVisibilityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		executionVisibilityOrders(run, 0, 1, 2, 3)
		for _, object := range executionVisibilityObjects()[:4] {
			var id int64
			if err := conn.QueryRowContext(p.Ctx, "SELECT ("+object.identity+")::bigint").Scan(&id); err != nil || id == 0 { t.Fatal(id, err) }
			query := "SELECT pg_catalog."+object.fn+"($1)"
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil { t.Fatal(err) }
			for _, path := range []string{"vex_a,vex_b", "vex_b,vex_a"} {
				run(fixture.Exec("SET search_path="+path, 0))
				s := executionVisibilityDecision(object.fn, "$1", path == "vex_a,vex_b"); s.Args = []any{id}; s.CheckMetadata = fixture.VisibilityBoolMetadata
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}, b.Recorder, s)
			}
			if err := stmt.Close(); err != nil { t.Fatal(err) }
			run(executionVisibilityDecision(object.fn, "NULL", nil))
			run(executionVisibilityDecision(object.fn, "0::oid", nil))
		}
		bad := fixture.ImplicitError("SELECT pg_function_is_visible(TRUE)", fixture.Diagnostic{Code:"42883", Message:"function pg_function_is_visible(boolean) does not exist", Position:"8", Hint:"No function matches the given name and argument types. You might need to add explicit type casts."})
		bad.SelectRows = true; bad.ErrorStage = "execute"
		run(bad)
		executionVisibilityStored(run, true)
		executionVisibilityRoundtrip(t, p, b, run)
	})
}

func executionVisibilityHeldViews(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
	t.Helper()
	for _, name := range []string{"function", "operator", "class", "family"} {
		query := "SELECT visible FROM public.vex_stored_"+name
		stmt, err := conn.PrepareContext(p.Ctx, query)
		if err != nil { t.Fatal(err) }
		for _, path := range []string{"vex_a,vex_b", "vex_b,vex_a", "public"} {
			run(fixture.Exec("SET search_path="+path, 0))
			s := fixture.Select(query, []string{"visible"}, []string{"BOOL"}, [][]any{{path == "vex_a,vex_b"}})
			s.CheckMetadata = fixture.VisibilityBoolMetadata
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: query}, b.Recorder, s)
		}
		if err := stmt.Close(); err != nil { t.Fatal(err) }
	}
}
