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
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT range_acl_source.many(range_acl_source.span(1,3,'[]'))::text AS m", []string{"m"}, []string{"TEXT"}, [][]any{{"{[1,3]}"}}))
			acl := fixture.Select("SELECT unnest(typacl)::text AS acl FROM pg_type WHERE oid='range_acl_source.span'::regtype", []string{"acl"}, []string{"TEXT"}, [][]any{{"range_acl_creator=U/range_acl_creator"}, {"range_acl_reader=U/range_acl_creator"}})
			acl.Bag = true
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, acl)
			for _, schema := range []string{"range_acl_source", "range_acl_companion"} {
				namespaceACL := fixture.Select("SELECT unnest(nspacl)::text AS acl FROM pg_namespace WHERE nspname=$1::name", []string{"acl"}, []string{"TEXT"}, [][]any{{"range_acl_creator=UC/range_acl_creator"}, {"range_acl_reader=UC/range_acl_creator"}, {"range_acl_outsider=UC/range_acl_creator"}}, schema)
				namespaceACL.Bag = true
				fixture.RunStep(t, p.Ctx, executor, backend.Recorder, namespaceACL)
			}
			for _, typ := range []string{"range_acl_source.span", "range_acl_source.span[]", "range_acl_companion.many", "range_acl_companion.many[]"} {
				fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT has_type_privilege('range_acl_reader',$1::text,'USAGE') AS reader,has_type_privilege('range_acl_reader',$1::text,'USAGE WITH GRANT OPTION') AS options,has_type_privilege('range_acl_outsider',$1::text,'USAGE') AS outsider", []string{"reader", "options", "outsider"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, false, false}}, typ))
			}
		}
		check(b, c)
		if !b.PG {
			var script bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
				t.Fatal(err)
			}
			const repair = "UPDATE pg_catalog.pg_type SET typacl=E'{range_acl_creator=U/range_acl_creator,range_acl_reader=U/range_acl_creator}'::pg_catalog.aclitem[] WHERE oid=E'range_acl_source.span'::pg_catalog.regtype;"
			if !strings.Contains(script.String(), repair) {
				t.Fatal("dump omitted exact range ACL repair", script.String())
			}
			stream := script.String()
			typeDefinition := strings.Index(stream, `CREATE TYPE "range_acl_source"."span"`)
			readerTable := strings.Index(stream, `CREATE TABLE "range_acl_companion"."held"`)
			firstRepair := strings.Index(stream, repair)
			if typeDefinition < 0 || firstRepair <= typeDefinition || readerTable <= firstRepair {
				t.Fatal("range authority must follow its declaration and precede nonowner consumers", stream)
			}
			for _, schema := range []string{"range_acl_source", "range_acl_companion"} {
				namespaceRepair := "UPDATE pg_catalog.pg_namespace SET nspacl=E'{range_acl_creator=UC/range_acl_creator,range_acl_reader=UC/range_acl_creator,range_acl_outsider=UC/range_acl_creator}'::pg_catalog.aclitem[] WHERE nspname=E'" + schema + "'::pg_catalog.name;"
				if at := strings.Index(stream, namespaceRepair); at < 0 || at >= typeDefinition {
					t.Fatal("schema authority must precede object recipes", stream)
				}
			}
			if !strings.Contains(stream, `"range_acl_source"."many"("range_acl_source"."span"(`) || strings.Contains(stream, `"range_acl_companion"."many"(`) {
				t.Fatal("multirange constructor must use its owning range namespace", stream)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(script.Bytes())); err != nil {
				t.Fatal(err)
			}
			check(b, c)
		}
	})
}

// Exercise column-creation authority in addition to privilege inquiries, so
// companion delegation is witnessed by actual direct and prepared consumers.
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
		fixture.RangeConsumeHeld(t, held, saved)
		run(fixture.Exec("REVOKE USAGE ON TYPE acl_baseline FROM PUBLIC", 0))
		checkACL := func() {
			run(fixture.Select("SELECT typacl IS NULL AS absent,cardinality(typacl) AS entries,typacl[1]::text=pg_get_userbyid(typowner)||'=U/'||pg_get_userbyid(typowner) AS owner_entry FROM pg_type WHERE oid='acl_baseline'::regtype", []string{"absent", "entries", "owner_entry"}, []string{"BOOL", "INT4", "BOOL"}, [][]any{{false, int64(1), true}}))
			for _, typ := range []string{"acl_baseline", "acl_baseline[]", "acl_baselines", "acl_baselines[]"} {
				run(fixture.Select("SELECT has_type_privilege('public',$1::text,'USAGE') AS usage", []string{"usage"}, []string{"BOOL"}, [][]any{{false}}, typ))
			}
		}
		checkACL()
		if !b.PG {
			var script bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
				t.Fatal(err)
			}
			const repair = "UPDATE pg_catalog.pg_type SET typacl=E'{postgres=U/postgres}'::pg_catalog.aclitem[] WHERE oid=E'public.acl_baseline'::pg_catalog.regtype;"
			if !strings.Contains(script.String(), repair) {
				t.Fatal("dump omitted exact revoked range ACL repair", script.String())
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(script.Bytes())); err != nil {
				t.Fatal(err)
			}
			checkACL()
			check(b, c)
		}
	})
}
