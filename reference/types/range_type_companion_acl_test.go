package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// PostgreSQL REL_18_6 aclchk.c delegates arrays then multiranges to their
// owning range, and rejects independent companion GRANT/REVOKE (0LP01).
func TestReferenceRangeTypeCompanionACL(t *testing.T) {
	t.Run("authorization", rangeTypeCompanionAuthorization)
	t.Run("baseline_replacement", rangeTypeCompanionBaseline)
}

func rangeTypeCompanionAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"range_acl_creator", "range_acl_reader", "range_acl_outsider"})
	fixture.SchemaReferenceCleanup(t, "range_acl_source")
	fixture.SchemaReferenceCleanup(t, "range_acl_companion")
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		defer c.ExecContext(p.Ctx, "RESET ROLE;RESET search_path")
		for _, q := range []string{
			"CREATE ROLE range_acl_creator", "CREATE ROLE range_acl_reader", "CREATE ROLE range_acl_outsider",
			"CREATE SCHEMA range_acl_source AUTHORIZATION range_acl_creator",
			"CREATE SCHEMA range_acl_companion AUTHORIZATION range_acl_creator",
			"GRANT USAGE,CREATE ON SCHEMA range_acl_source,range_acl_companion TO range_acl_reader,range_acl_outsider",
			"SET ROLE range_acl_creator",
			"ALTER DEFAULT PRIVILEGES IN SCHEMA range_acl_companion REVOKE USAGE ON TYPES FROM PUBLIC",
			"CREATE TYPE range_acl_source.span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=range_acl_companion.many)",
			"CREATE TABLE range_acl_source.saved(k integer,r range_acl_source.span,m range_acl_companion.many)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO range_acl_source.saved VALUES(1,'[1,3]','{[1,3],[8,9]}')", 1))
		run(fixture.Exec("RESET ROLE", 0))
		usage := func(want bool) {
			rangeTypeCompanionUse(t, p, b, c, "range_acl_reader", want)
		}
		usage(true)
		heldQuery := "CREATE TABLE range_acl_companion.held(v range_acl_companion.many[])"
		held, err := c.PrepareContext(p.Ctx, heldQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		run(fixture.Exec("SET ROLE range_acl_creator", 0))
		run(fixture.Exec("REVOKE USAGE ON TYPE range_acl_source.span FROM PUBLIC,range_acl_reader", 0))
		run(fixture.Exec("RESET ROLE", 0))
		usage(false)
		run(fixture.Exec("SET ROLE range_acl_reader", 0))
		run(fixture.Exec("SET search_path=range_acl_source,range_acl_companion,public", 0))
		for _, tc := range []struct{ name, typ string }{{"r", "span"}, {"m", "many"}, {"a", "many[]"}} {
			q := "CREATE TABLE range_acl_companion.denied_" + tc.name + "(v " + tc.typ + ")"
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for type " + strings.TrimSuffix(tc.typ, "[]")})
			s.ErrorStage = "execute"
			rangeTypeRemedyRoutes(t, p, b, c, s)
		}
		heldDenied := fixture.ImplicitError(heldQuery, fixture.Diagnostic{Code: "42501", Message: "permission denied for type many"})
		heldDenied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: heldQuery}, b.Recorder, heldDenied)
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("RESET search_path", 0))
		run(fixture.Exec("SET ROLE range_acl_creator", 0))
		for _, q := range []string{"GRANT USAGE ON TYPE range_acl_companion.many TO range_acl_reader", "REVOKE USAGE ON TYPE range_acl_companion.many FROM PUBLIC"} {
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0LP01", Message: "cannot set privileges of multirange types", Hint: "Set the privileges of the range type instead."})
			s.ErrorStage = "execute"
			rangeTypeRemedyRoutes(t, p, b, c, s)
		}
		run(fixture.Exec("GRANT USAGE ON TYPE range_acl_source.span TO range_acl_reader", 0))
		run(fixture.Exec("RESET ROLE", 0))
		usage(true)
		run(fixture.Exec("SET ROLE range_acl_reader", 0))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: heldQuery}, b.Recorder, fixture.Exec(heldQuery, 0))
		for _, tc := range []struct{ name, typ string }{{"r", "range_acl_source.span"}, {"m", "range_acl_companion.many"}, {"a", "range_acl_companion.many[]"}} {
			run(fixture.Exec("CREATE TABLE range_acl_companion.allowed_"+tc.name+"(v "+tc.typ+")", 0))
			s := fixture.Prepared(fixture.Exec("INSERT INTO range_acl_companion.allowed_"+tc.name+" VALUES($1)", 1, nil))
			run(s)
		}
		run(fixture.Exec("RESET ROLE", 0))
		check := func(backend fixture.Backend, executor fixture.Executor) {
			rangeTypeCompanionUse(t, p, backend, executor, "range_acl_reader", true)
			rangeTypeCompanionUse(t, p, backend, executor, "range_acl_outsider", false)
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT k,r::text AS r,m::text AS m FROM range_acl_source.saved", []string{"k", "r", "m"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "[1,3]", "{[1,3],[8,9]}"}}))
		}
		check(b, c)
		if !b.PG {
			var rejected bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &rejected); err == nil || !strings.Contains(err.Error(), "dump cannot serialize") || rejected.Len() != 0 {
				t.Fatal("explicit ACL must reject before output", err, rejected.String())
			}
			check(b, c)
		}
	})
}

// Type USAGE has no executable privilege-inquiry builtin in the admitted
// inventory. Exercise the actual column-creation authority, not a fake inquiry.
func rangeTypeCompanionUse(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, role string, allowed bool) {
	t.Helper()
	if db, ok := executor.(*sql.DB); ok {
		conn, err := db.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		executor = conn
	}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, executor, b.Recorder, s) }
	run(fixture.Exec("SET ROLE "+role, 0))
	run(fixture.Exec("SET search_path=range_acl_source,range_acl_companion,public", 0))
	defer executor.ExecContext(p.Ctx, "RESET ROLE;RESET search_path")
	for _, tc := range []struct{ name, typ string }{{"r", "span"}, {"m", "many"}, {"a", "many[]"}} {
		q := "CREATE TABLE range_acl_companion.probe_" + tc.name + "(v " + tc.typ + ")"
		for _, prepared := range []bool{false, true} {
			s := fixture.Exec(q, 0)
			if !allowed {
				s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for type " + strings.TrimSuffix(tc.typ, "[]")})
				s.ErrorStage = "execute"
			}
			s.Prepared = prepared
			run(s)
			if allowed {
				run(fixture.Exec("DROP TABLE range_acl_companion.probe_"+tc.name, 0))
			}
		}
	}
}

func rangeTypeCompanionBaseline(t *testing.T) {
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec("CREATE TYPE acl_baseline AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=acl_baselines)", 0))
		run(fixture.Exec("CREATE TABLE acl_baseline_saved(k integer,r acl_baseline,m acl_baselines,a acl_baselines[])", 0))
		run(fixture.Exec(`INSERT INTO acl_baseline_saved VALUES(1,'[1,3]','{[1,3],[8,9]}',ARRAY['{[1,3],[8,9]}'::acl_baselines])`, 1))
		saved := fixture.Select("SELECT k,r::text AS r,m::text AS m,a[1]::text AS a FROM acl_baseline_saved", []string{"k", "r", "m", "a"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "[1,3]", "{[1,3],[8,9]}", "{[1,3],[8,9]}"}})
		check := func(backend fixture.Backend, executor fixture.Executor) {
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, saved)
			for _, typ := range []string{"acl_baseline", "acl_baselines", "acl_baselines[]"} {
				for _, prepared := range []bool{false, true} {
					step := fixture.Exec("CREATE TABLE acl_baseline_probe(v "+typ+")", 0)
					step.Prepared = prepared
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, step)
					fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Exec("DROP TABLE acl_baseline_probe", 0))
				}
			}
		}
		check(b, c)
		held, err := c.QueryContext(p.Ctx, saved.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		if !b.PG {
			heldCreate, err := c.PrepareContext(p.Ctx, "CREATE TABLE acl_baseline_held(v acl_baselines[])")
			if err != nil {
				t.Fatal(err)
			}
			defer heldCreate.Close()
			var script bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(script.Bytes())); err != nil {
				t.Fatal(err)
			}
			result, err := heldCreate.ExecContext(p.Ctx)
			if err != nil {
				t.Fatal("held companion-array type-use after replacement", err)
			}
			if affected, err := result.RowsAffected(); err != nil || affected != 0 {
				t.Fatal(affected, err)
			}
			run(fixture.Exec("DROP TABLE acl_baseline_held", 0))
			check(b, c)
		}
		rangeTypeRemedyReplay(t, p, b, c, nil, check, fixture.Step{}, nil)
		rangeConsumeHeld(t, held, saved)
		if !b.PG {
			run(fixture.Exec("REVOKE USAGE ON TYPE acl_baseline FROM PUBLIC", 0))
			var rejected bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &rejected); err == nil || !strings.Contains(err.Error(), "changed type ACL") || rejected.Len() != 0 {
				t.Fatal("range ACL must not be silently omitted", err, rejected.String())
			}
			check(b, c)
		}
	})
}
