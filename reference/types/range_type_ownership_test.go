package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeOwnership(t *testing.T) {
	roles := []string{"range_owner_first", "range_owner_next", "range_owner_outsider"}
	fixture.RoleReferenceCleanup(t, roles)
	fixture.SchemaReferenceCleanup(t, "range_owner_space")
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
		defer c.ExecContext(p.Ctx, "RESET ROLE; RESET search_path")
		for _, role := range roles {
			run(fixture.Exec("CREATE ROLE "+role, 0))
		}
		run(fixture.Exec("CREATE SCHEMA range_owner_space AUTHORIZATION range_owner_outsider", 0))
		run(fixture.Exec("SET search_path=range_owner_space,public", 0))
		for _, q := range []string{
			"CREATE TYPE owner_span AS RANGE(SUBTYPE=int4,MULTIRANGE_TYPE_NAME=owner_many)",
			"CREATE TYPE owner_unrelated AS ENUM('keep')",
			"CREATE TABLE owner_saved(k integer,r owner_span,m owner_many)",
			"CREATE TABLE owner_sink(m owner_many)",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO owner_saved VALUES(1,'[1,3]','{[1,3],[8,9]}'),(2,'[1,4)','{}')", 2))
		saved := fixture.Select("SELECT k,r::text AS r,m::text AS m FROM range_owner_space.owner_saved ORDER BY k", []string{"k", "r", "m"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{int64(1), "[1,3]", "{[1,3],[8,9]}"}, {int64(2), "[1,4)", "{}"}})
		checkOwner := func(backend fixture.Backend, executor fixture.Executor, owner string) {
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT typname,typowner=(SELECT oid FROM pg_roles WHERE rolname="+rangeLiteral(owner)+") AS correct_owner FROM pg_type WHERE typnamespace='range_owner_space'::regnamespace AND typname IN ('owner_span','_owner_span','owner_many','_owner_many') ORDER BY typname", []string{"typname", "correct_owner"}, []string{"NAME", "BOOL"}, [][]any{{"_owner_many", true}, {"_owner_span", true}, {"owner_many", true}, {"owner_span", true}}))
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='range_owner_space'::regnamespace AND proname IN ('owner_span','owner_many') AND proowner=(SELECT typowner FROM pg_type WHERE oid='bool'::regtype)", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, fixture.Select("SELECT castfunc='range_owner_space.owner_many(range_owner_space.owner_span)'::regprocedure::oid AS constructor,castcontext::text AS context,castmethod::text AS method FROM pg_cast WHERE castsource='range_owner_space.owner_span'::regtype AND casttarget='range_owner_space.owner_many'::regtype", []string{"constructor", "context", "method"}, []string{"BOOL", "TEXT", "TEXT"}, [][]any{{true, "e", "f"}}))
			fixture.RunStep(t, p.Ctx, executor, backend.Recorder, saved)
		}
		run(fixture.Exec("ALTER TYPE owner_span OWNER TO range_owner_first", 0))
		checkOwner(b, c, "range_owner_first")
		run(fixture.Prepared(fixture.Exec("ALTER TYPE owner_span OWNER TO range_owner_next", 0)))
		checkOwner(b, c, "range_owner_next")
		run(fixture.Exec("SET ROLE range_owner_outsider", 0))
		denied := fixture.ImplicitError("ALTER TYPE range_owner_space.owner_span OWNER TO range_owner_first", fixture.Diagnostic{Code: "42501", Message: "must be owner of type owner_span"})
		denied.ErrorStage = "execute"
		rangeTypeRemedyRoutes(t, p, b, c, denied)
		run(fixture.Exec("RESET ROLE", 0))
		checkOwner(b, c, "range_owner_next")
		companion := fixture.ImplicitError("ALTER TYPE owner_many OWNER TO range_owner_first", fixture.Diagnostic{Code: "42809", Message: "cannot alter multirange type owner_many", Hint: "You can alter type owner_span, which will alter the multirange type as well."})
		companion.ErrorStage = "execute"
		rangeTypeRemedyRoutes(t, p, b, c, companion)
		checkOwner(b, c, "range_owner_next")
		run(fixture.Exec("REASSIGN OWNED BY range_owner_next TO range_owner_first", 0))
		checkOwner(b, c, "range_owner_first")
		run(fixture.Exec("DROP OWNED BY range_owner_next RESTRICT", 0))
		checkOwner(b, c, "range_owner_first")
		held, err := c.QueryContext(p.Ctx, saved.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		check := func(backend fixture.Backend, executor fixture.Executor) {
			checkOwner(backend, executor, "range_owner_first")
		}
		if !b.PG {
			rangeTypeOwnershipPairControls(t, p, roles, check)
		}
		rangeTypeRemedyReplay(t, p, b, c, roles, check, fixture.Prepared(fixture.Exec("INSERT INTO range_owner_space.owner_sink VALUES($1::range_owner_space.owner_many)", 1, "{[20,21]}")), []string{"range_owner_space"})
		fixture.RangeConsumeHeld(t, held, saved)
		restricted := fixture.ImplicitError("DROP OWNED BY range_owner_first RESTRICT", fixture.Diagnostic{Code: "2BP01", Message: "cannot drop type owner_span because other objects depend on it", Detail: "column m of table owner_saved depends on type owner_many\ncolumn m of table owner_sink depends on type owner_many\ncolumn r of table owner_saved depends on type owner_span", Hint: "Use DROP ... CASCADE to drop the dependent objects too."})
		restricted.ErrorStage = "execute"
		rangeTypeRemedyRoutes(t, p, b, c, restricted)
		checkOwner(b, c, "range_owner_first")
		run(fixture.Exec("DROP OWNED BY range_owner_first CASCADE", 0))
		rangeTypeAbsent(run, "owner_span", "_owner_span", "owner_many", "_owner_many")
		run(fixture.Select("SELECT k FROM owner_saved ORDER BY k", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		run(fixture.Select("SELECT proname FROM pg_proc WHERE pronamespace='range_owner_space'::regnamespace AND proname IN ('owner_span','owner_many')", []string{"proname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT 'keep'::owner_unrelated::text AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"keep"}}))
	})
}

func rangeTypeOwnershipPairControls(t *testing.T, p *fixture.Pair, roles []string, check func(fixture.Backend, fixture.Executor)) {
	t.Helper()
	var script bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &script); err != nil {
		t.Fatal(err)
	}
	actual := script.String()
	start := strings.Index(actual, "ALTER TYPE ")
	if start < 0 {
		t.Fatal("owned range dump omitted its ordinary owner transfer")
	}
	end := strings.IndexByte(actual[start:], '\n')
	if end < 0 {
		t.Fatal("unterminated range owner directive")
	}
	ownerLine := actual[start : start+end+1]
	target := fixture.NewFixture(t, false, true)
	conn, err := target.Memory.Conn(target.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	run := func(s fixture.Step) { fixture.RunStep(t, target.Ctx, conn, target.MemoryRecord, s) }
	for _, role := range roles {
		run(fixture.Exec("CREATE ROLE "+role, 0))
	}
	run(fixture.Exec("CREATE TABLE owner_pair_sentinel(k integer)", 0))
	run(fixture.Exec("INSERT INTO owner_pair_sentinel VALUES(42)", 1))
	sentinel := fixture.Select("SELECT k FROM owner_pair_sentinel", []string{"k"}, []string{"INT4"}, [][]any{{int64(42)}})
	held, err := conn.QueryContext(target.Ctx, sentinel.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var before bytes.Buffer
	if err := stuntgres.Dump(target.Ctx, target.Native, &before); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, line string }{
		{"wrong_owner", "ALTER TYPE range_owner_space.owner_span OWNER TO range_owner_next;\n"},
		{"wrong_family_member", "ALTER TYPE range_owner_space.owner_many OWNER TO range_owner_first;\n"},
		{"wrong_kind", "ALTER DOMAIN range_owner_space.owner_span OWNER TO range_owner_first;\n"},
		{"wrong_namespace", "ALTER TYPE public.owner_span OWNER TO range_owner_first;\n"},
		{"unqualified", "ALTER TYPE owner_span OWNER TO range_owner_first;\n"},
		{"expired", "CREATE TABLE public.owner_pair_gap(k integer);\n" + ownerLine},
		{"consumed", ownerLine + ownerLine},
	} {
		forged := strings.Replace(actual, ownerLine, tc.line, 1)
		err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(forged))
		var diagnostic interface{ SQLState() string }
		if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "0A000" {
			t.Fatal(tc.name, "forged owner pair was admitted or failed outside its owning boundary", err)
		}
		var after bytes.Buffer
		if err := stuntgres.Dump(target.Ctx, target.Native, &after); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal(tc.name, "forged pair changed target state")
		}
		run(sentinel)
	}
	fixture.RangeConsumeHeld(t, held, sentinel)
	if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(actual)); err != nil {
		t.Fatal("exact generated owner pair rejected", err)
	}
	check(fixture.Backend{DB: target.Memory, Recorder: target.MemoryRecord}, conn)
}
