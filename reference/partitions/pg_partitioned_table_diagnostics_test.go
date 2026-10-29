package reference_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGPartitionedTableDiagnostics(t *testing.T) {
	pptScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		pool, _, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.DB, b.Recorder = pool, recorder
		defer func() {
			if len(notices) != 0 {
				t.Fatal("unexpected notices", notices)
			}
		}()
		run := pptRun(t, p, b, b.DB)
		for _, q := range []string{"CREATE TABLE ppt_sentinel(v INT)", "CREATE TABLE ppt_good(a INT) PARTITION BY RANGE(a)", "CREATE TABLE ppt_good_default PARTITION OF ppt_good DEFAULT"} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO ppt_sentinel VALUES(7)", 1))
		check := func() {
			pptDefault(run, "ppt_good", "ppt_good_default")
			run(fixture.Select("SELECT v FROM ppt_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
			pptBoth(run, fixture.Select("SELECT relname FROM pg_class WHERE relname='ppt_bad'", []string{"relname"}, []string{"NAME"}, nil))
		}
		for _, tc := range []struct{ q, position string }{
			{"SELECT missing FROM pg_catalog.pg_partitioned_table", "8"},
			{"SELECT /*é*/ missing FROM pg_catalog.pg_partitioned_table", "14"},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: tc.position})
				s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				check()
			}
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.ImplicitError("CREATE TABLE ppt_bad(a INT,b INT) PARTITION BY LIST(a,b)", fixture.Diagnostic{Code: "42P17", Message: `cannot use "list" partition strategy with more than one column`})
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			check()
		}
		pptEmpty(run, b, "SELECT * FROM pg_catalog.pg_partitioned_table WHERE partrelid=$1::oid", int64(4294967295))
	})
}

func TestReferencePGPartitionedTablePermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ppt_reader"})
	fixture.SchemaReferenceCleanup(t, "ppt_private")
	pptScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		pool, _, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.DB, b.Recorder = pool, recorder
		defer func() {
			if len(notices) != 0 {
				t.Fatal("unexpected notices", notices)
			}
		}()
		c := fixture.SequenceCoreConn(t, p.Ctx, b.DB)
		run := pptRun(t, p, b, c)
		for _, q := range []string{
			"CREATE ROLE ppt_reader", "CREATE SCHEMA ppt_private", "REVOKE ALL ON SCHEMA ppt_private FROM PUBLIC",
			"CREATE TABLE ppt_private.ppt_acl(a INT) PARTITION BY RANGE(a)",
			"CREATE TABLE ppt_private.ppt_acl_default PARTITION OF ppt_private.ppt_acl DEFAULT",
			"CREATE TABLE public.ppt_acl_sentinel(v INT)",
			"GRANT USAGE ON SCHEMA public TO ppt_reader",
			"GRANT SELECT ON public.ppt_acl_sentinel TO ppt_reader",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO public.ppt_acl_sentinel VALUES(7)", 1))
		id, def := pptID(t, p, c, "ppt_private.ppt_acl"), pptID(t, p, c, "ppt_private.ppt_acl_default")
		q := `SELECT partstrat,partdefid::bigint,partattrs::text FROM pg_catalog.pg_partitioned_table WHERE partrelid=$1::oid`
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		heldRead := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		state := fixture.Select(q, []string{"partstrat", "partdefid", "partattrs"}, []string{"CHAR", "INT8", "TEXT"}, [][]any{{"r", def, "1"}}, id)
		check := func() {
			pptBoth(run, state)
			fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, state)
			run(fixture.Select("SELECT v FROM public.ppt_acl_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		}
		mutations := []string{
			"INSERT INTO pg_catalog.pg_partitioned_table SELECT * FROM pg_catalog.pg_partitioned_table WHERE partrelid=" + fmt.Sprint(id),
			"INSERT INTO pg_catalog.pg_partitioned_table SELECT * FROM pg_catalog.pg_partitioned_table WHERE FALSE",
			"UPDATE pg_catalog.pg_partitioned_table SET partdefid=0 WHERE partrelid=" + fmt.Sprint(id),
			"UPDATE pg_catalog.pg_partitioned_table SET partdefid=0 WHERE FALSE",
			"DELETE FROM pg_catalog.pg_partitioned_table WHERE partrelid=" + fmt.Sprint(id),
			"DELETE FROM pg_catalog.pg_partitioned_table WHERE FALSE",
		}
		commands := make([]fixture.HeldStatement, len(mutations))
		for i, q := range mutations {
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("owner prepare", err)
			}
			defer s.Close()
			commands[i] = fixture.HeldStatement{Executor: c, Statement: s, SQL: q}
		}
		run(fixture.Exec("SET ROLE ppt_reader", 0))
		defer c.ExecContext(context.Background(), "RESET ROLE")
		check() // Neither target-table SELECT nor target namespace USAGE was granted.
		type route struct {
			e        fixture.Executor
			prepared bool
		}
		for i, q := range mutations {
			s, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("reader prepare", err)
			}
			defer s.Close()
			for _, r := range []route{{c, false}, {c, true}, {commands[i], false}, {fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, false}} {
				step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table pg_partitioned_table"})
				step.Prepared, step.ErrorStage = r.prepared, "execute"
				fixture.RunStep(t, p.Ctx, r.e, b.Recorder, step)
				check()
			}
		}
		run(fixture.Exec("RESET ROLE", 0))
		// Real PostgreSQL owner catalog writes must never be executed.
		if !b.PG {
			for i, q := range mutations {
				for _, r := range []route{{c, false}, {c, true}, {commands[i], false}} {
					step := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "catalog source mutation is unsupported"})
					step.Prepared, step.ErrorStage = r.prepared, "execute"
					fixture.RunStep(t, p.Ctx, r.e, b.Recorder, step)
					check()
				}
			}
		}
	})
}
