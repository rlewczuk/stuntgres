package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceXML2XSLTIdentity(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"xslt_reader", "xslt_denied", "xslt_owner"})
	fixture.SchemaReferenceCleanup(t, "xslt_space")
	xml2Case(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		xml2Install(t, p, b, c)
		xsltControlSetup(t, p, b, c)
		for _, q := range []string{
			"CREATE ROLE xslt_reader", "CREATE ROLE xslt_denied", "CREATE ROLE xslt_owner",
			"CREATE TABLE xslt_acl_null(doc text)",
			"GRANT USAGE ON SCHEMA public TO xslt_reader,xslt_denied",
			"GRANT SELECT ON xslt_values,xslt_typed,xslt_saved,xslt_acl_null TO xslt_reader,xslt_denied",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO xslt_acl_null VALUES(NULL)", 1))
		two, err := c.PrepareContext(p.Ctx, xsltTwoQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer two.Close()
		three, err := c.PrepareContext(p.Ctx, xsltThreeQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer three.Close()
		h2 := fixture.HeldStatement{Executor: c, Statement: two, SQL: xsltTwoQuery}
		h3 := fixture.HeldStatement{Executor: c, Statement: three, SQL: xsltThreeQuery}
		for _, signature := range []string{"text,text", "text,text,text"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("REVOKE EXECUTE ON FUNCTION xslt_process("+signature+") FROM PUBLIC", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xslt_reader", 0))
			query, held, args := xsltTwoQuery, h2, []any{`<r>A</r>`, xsltControlStyle()}
			if signature == "text,text,text" {
				query, held, args = xsltThreeQuery, h3, append(args, "p='3'")
			}
			denied := xml2Error("42501", "permission denied for function xslt_process")
			xml2Failure(t, p, b, c, query, denied, false, args...)
			s := fixture.ErrorDiagnostic(query, denied, args...)
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			// A surviving row-bound NULL remains subject to ordinary EXECUTE ACLs.
			xml2Failure(t, p, b, c, "SELECT xslt_process(doc,$1::text"+xsltACLThird(signature)+") AS v FROM xslt_acl_null", denied, false, xsltControlStyle())
			xml2Failure(t, p, b, c, "SELECT two,three FROM xslt_values", denied, false)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("GRANT EXECUTE ON FUNCTION xslt_process("+signature+") TO xslt_reader", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xslt_reader", 0))
			want := "A2"
			if signature == "text,text,text" {
				want = "A3"
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"v"}, []string{"TEXT"}, [][]any{{want}}, args...))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		}
		for _, signature := range []string{"text,text", "text,text,text"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER FUNCTION xslt_process("+signature+") OWNER TO xslt_owner", 0))
		}
		xml2Both(t, p, b, c, fixture.Select("SELECT p.pronargs::integer AS n,r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.proname='xslt_process' ORDER BY p.pronargs", []string{"n", "rolname"}, []string{"INT4", "NAME"}, [][]any{{int64(2), "xslt_owner"}, {int64(3), "xslt_owner"}}))
		for _, q := range []string{
			"CREATE SCHEMA xslt_space", "GRANT USAGE ON SCHEMA xslt_space TO xslt_reader,xslt_denied",
			"ALTER FUNCTION xslt_process(text,text) RENAME TO xslt_two",
			"ALTER FUNCTION xslt_process(text,text,text) RENAME TO xslt_three",
			"ALTER FUNCTION xslt_two(text,text) SET SCHEMA xslt_space",
			"ALTER FUNCTION xslt_three(text,text,text) SET SCHEMA xslt_space",
			"ALTER EXTENSION xml2 DROP FUNCTION xslt_space.xslt_two(text,text)",
			"ALTER EXTENSION xml2 DROP FUNCTION xslt_space.xslt_three(text,text,text)",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			xsltControlValues(t, p, b, c)
			xsltControlFresh(t, p, b, c)
		}
		// Old SQL may resolve new ordinary routines; already stored dependencies
		// keep their original provider identities rather than following spelling.
		for _, q := range []string{
			"CREATE FUNCTION xslt_process(text,text) RETURNS text LANGUAGE SQL IMMUTABLE AS 'SELECT ''shadow2''::text'",
			"CREATE FUNCTION xslt_process(text,text,text) RETURNS text LANGUAGE SQL VOLATILE AS 'SELECT ''shadow3''::text'",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, h2, b.Recorder, xsltValue(`<r>A</r>`, xsltControlStyle(), "shadow2"))
		fixture.RunStep(t, p.Ctx, h3, b.Recorder, xsltParamValue(`<r>A</r>`, xsltControlStyle(), "p='3'", "shadow3"))
		xsltControlFresh(t, p, b, c)
		check := func(b fixture.Backend, c *sql.Conn) {
			t.Helper()
			xml2Both(t, p, b, c, fixture.Select("SELECT p.proname,r.rolname FROM pg_proc p JOIN pg_roles r ON r.oid=p.proowner JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='xslt_space' ORDER BY p.proname", []string{"proname", "rolname"}, []string{"NAME", "NAME"}, [][]any{{"xslt_three", "xslt_owner"}, {"xslt_two", "xslt_owner"}}))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xslt_reader", 0))
			xml2Both(t, p, b, c, fixture.Select("SELECT xslt_space.xslt_two($1::text,$2::text) AS two,xslt_space.xslt_three($1::text,$2::text,$3::text) AS three", []string{"two", "three"}, []string{"TEXT", "TEXT"}, [][]any{{"R2", "R3"}}, `<r>R</r>`, xsltControlStyle(), "p='3'"))
			xsltControlValues(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE xslt_denied", 0))
			xml2Failure(t, p, b, c, "SELECT xslt_space.xslt_two($1::text,$2::text)", xml2Error("42501", "permission denied for function xslt_two"), false, `<r/>`, xsltControlStyle())
			xml2Failure(t, p, b, c, "SELECT xslt_space.xslt_three($1::text,$2::text,$3::text)", xml2Error("42501", "permission denied for function xslt_three"), false, `<r/>`, xsltControlStyle(), "")
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		}
		check(b, c)
		if b.PG {
			return
		}
		dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		check(b, c)
		xsltControlFresh(t, p, b, c)
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("xslt_acl_destination")
		if err != nil {
			t.Fatal(err)
		}
		control := sql.OpenDB(connector)
		defer control.Close()
		pool, recorder := fixture.RecordedNativePool(t, connector)
		dst, err := pool.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer dst.Close()
		destination := fixture.Backend{Name: "destination", DB: pool, Recorder: recorder}
		for _, role := range []string{"xslt_reader", "xslt_denied", "xslt_owner"} {
			fixture.RunStep(t, p.Ctx, dst, recorder, fixture.Exec("CREATE ROLE "+role, 0))
		}
		if err := stuntgres.Restore(p.Ctx, control, strings.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		check(destination, dst)
		xsltControlFresh(t, p, destination, dst)
	})
}

func xsltACLThird(signature string) string {
	if signature == "text,text,text" {
		return ",'p=''3'''"
	}
	return ""
}
