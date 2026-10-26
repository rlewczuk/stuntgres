package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func ltxtqueryIdentityChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT oid,typarray,typinput::oid,typoutput::oid,typnamespace,typowner FROM pg_type WHERE oid=('ltxtquery'::regtype)::oid")
	if err != nil { t.Fatal(err) }
	var scalar, array, input, output, namespace, owner int64
	if !rows.Next() { rows.Close(); t.Fatal("missing installed ltxtquery") }
	if err := rows.Scan(&scalar, &array, &input, &output, &namespace, &owner); err != nil { rows.Close(); t.Fatal(err) }
	if rows.Next() { rows.Close(); t.Fatal("duplicate ltxtquery identity") }
	if err := rows.Err(); err != nil { rows.Close(); t.Fatal(err) }
	rows.Close()
	if scalar <= 0 || array <= 0 || input <= 0 || output <= 0 || scalar == array || input == output { t.Fatalf("invalid installed identities %d/%d/%d/%d", scalar, array, input, output) }
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, s := range []fixture.Step{
		fixture.Select("SELECT extname,extversion,extrelocatable FROM pg_extension WHERE extname='ltree'", []string{"extname", "extversion", "extrelocatable"}, []string{"NAME", "TEXT", "BOOL"}, [][]any{{"ltree", "1.3", true}}),
		fixture.Select(fmt.Sprintf("SELECT typname,typtype,typlen,typbyval,typalign,typstorage,typcategory,typispreferred,typcollation::text AS typcollation,typtypmod FROM pg_type WHERE oid=%d", scalar), []string{"typname", "typtype", "typlen", "typbyval", "typalign", "typstorage", "typcategory", "typispreferred", "typcollation", "typtypmod"}, []string{"NAME", "CHAR", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "BOOL", "TEXT", "INT4"}, [][]any{{"ltxtquery", "b", int64(-1), false, "i", "x", "U", false, "0", int64(-1)}}),
		fixture.Select(fmt.Sprintf("SELECT typname,typelem=%d AS element_identity,typnamespace=%d AS same_namespace,typowner=%d AS same_owner FROM pg_type WHERE oid=%d", scalar, namespace, owner, array), []string{"typname", "element_identity", "same_namespace", "same_owner"}, []string{"NAME", "BOOL", "BOOL", "BOOL"}, [][]any{{"_ltxtquery", true, true, true}}),
		fixture.Select(fmt.Sprintf("SELECT deptype FROM pg_depend WHERE classid=1247 AND objid=%d AND refclassid=1247 AND refobjid=%d", array, scalar), []string{"deptype"}, []string{"CHAR"}, [][]any{{"i"}}),
		fixture.Select(fmt.Sprintf("SELECT deptype,refobjid=(SELECT oid FROM pg_extension WHERE extname='ltree') AS extension_identity FROM pg_depend WHERE classid=1247 AND objid=%d AND refclassid=3079", scalar), []string{"deptype", "extension_identity"}, []string{"CHAR", "BOOL"}, [][]any{{"e", true}}),
	} { run(s) }
	for _, codec := range []struct{ oid int64; name, args, result string }{
		{input, "ltxtq_in", "2275", fmt.Sprint(scalar)},
		{output, "ltxtq_out", fmt.Sprint(scalar), "2275"},
	} {
		run(fixture.Select(fmt.Sprintf("SELECT proname,proargtypes::text AS arguments,prorettype::oid::text AS result,proisstrict,provolatile,proparallel,pronamespace=%d AS same_namespace,proowner=%d AS same_owner,prolang=(SELECT oid FROM pg_language WHERE lanname='c') AS c_language FROM pg_proc WHERE oid=%d", namespace, owner, codec.oid), []string{"proname", "arguments", "result", "proisstrict", "provolatile", "proparallel", "same_namespace", "same_owner", "c_language"}, []string{"NAME", "TEXT", "TEXT", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{codec.name, codec.args, codec.result, true, "i", "s", true, true, true}}))
		run(fixture.Select(fmt.Sprintf("SELECT deptype,refobjid=(SELECT oid FROM pg_extension WHERE extname='ltree') AS extension_identity FROM pg_depend WHERE classid=1255 AND objid=%d AND refclassid=3079", codec.oid), []string{"deptype", "extension_identity"}, []string{"CHAR", "BOOL"}, [][]any{{"e", true}}))
	}
}

func TestReferenceLtxtqueryInstallAndIdentity(t *testing.T) {
	t.Run("install", func(t *testing.T) {
		ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, prepared := range []bool{false, true} {
				q := "CREATE TABLE lt_absent(v ltxtquery)"
				want := fixture.ControlsReferenceDiagnostic("42704", `type "ltxtquery" does not exist`); want.Position = ltreeSQLPosition(q, "ltxtquery")
				ltreeError(t, p, b, c, q, prepared, false, false, want)
			}
			ltreeInstall(t, p, b, c)
			ltxtqueryIdentityChecks(t, p, b, c)
			var before, qualified, after int64
			if err := c.QueryRowContext(p.Ctx, "SELECT ('ltxtquery'::regtype)::oid").Scan(&before); err != nil { t.Fatal(err) }
			if err := c.QueryRowContext(p.Ctx, "SELECT ('public.ltxtquery'::regtype)::oid").Scan(&qualified); err != nil { t.Fatal(err) }
			if before <= 0 || before != qualified { t.Fatalf("identity %d/%d", before, qualified) }
			for _, prepared := range []bool{false, true} { ltreeError(t, p, b, c, "CREATE EXTENSION ltree", prepared, false, false, fixture.ControlsReferenceDiagnostic("42710", `extension "ltree" already exists`)) }
			var notices []fixture.Diagnostic
			_, nc, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			fixture.RunStep(t, p.Ctx, nc, recorder, fixture.Exec("CREATE EXTENSION IF NOT EXISTS ltree", 0))
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "ltree" already exists, skipping`}
			if len(notices) != 1 || notices[0] != want { t.Fatalf("notices %#v want %#v", notices, want) }
			if err := c.QueryRowContext(p.Ctx, "SELECT ('ltxtquery'::regtype)::oid").Scan(&after); err != nil { t.Fatal(err) }
			if after != before { t.Fatalf("IF NOT EXISTS replaced identity %d/%d", before, after) }
		})
	})
	t.Run("rollback", func(t *testing.T) {
		ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("BEGIN", 0)); ltreeInstall(t, p, b, c)
			run(ltxtquerySelect(b, "SELECT 'a'::ltxtquery AS v", []string{"v"}, [][]any{{"a"}}))
			run(fixture.Exec("ROLLBACK", 0))
			run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='ltree'", []string{"extname"}, []string{"NAME"}, [][]any{}))
			want := fixture.ControlsReferenceDiagnostic("42704", `type "ltxtquery" does not exist`); want.Position = "13"
			ltreeError(t, p, b, c, "SELECT 'a'::ltxtquery", false, true, true, want)
		})
	})
	t.Run("qualified", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "lx_namespace")
		ltreeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE SCHEMA lx_namespace", 0))
			run(fixture.Exec("CREATE EXTENSION ltree SCHEMA lx_namespace VERSION '1.3'", 0))
			run(fixture.Exec("CREATE TABLE public.lt_qualified(v lx_namespace.ltxtquery)", 0))
			run(fixture.Exec("SET search_path=lx_namespace,public", 0))
			run(fixture.Exec("CREATE TABLE public.lt_path(v ltxtquery)", 0))
			run(fixture.Exec("INSERT INTO public.lt_path VALUES ('a'),(NULL)", 2))
			s := ltxtquerySelect(b, "SELECT v FROM public.lt_path", []string{"v"}, [][]any{{"a"}, {nil}}); s.Bag = true; run(s)
			ltxtqueryIdentityChecks(t, p, b, c)
		})
	})
}
