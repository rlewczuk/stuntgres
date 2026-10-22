package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceOperatorFamilyDefinitions(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "family_definition_dest")
	familyScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.Prepared(fixture.Exec(familyCreate, 0)))
		run(fixture.Exec(`CREATE OPERATOR FAMILY public.family_probe USING hash`, 0))
		var before int64
		if err := conn.QueryRowContext(ctx, `SELECT oid FROM pg_opfamily WHERE opfname='family_probe' AND opfmethod=403 AND opfnamespace='public'::regnamespace`).Scan(&before); err != nil {
			t.Fatal(err)
		}
		if before == 0 {
			t.Fatal("zero family identity")
		}
		run(fixture.Exec(familyPopulate, 0))
		run(familyOperatorStep())
		run(fixture.Select(`SELECT count(*) FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE f.opfname='family_probe' AND f.opfmethod=405 AND f.opfnamespace='public'::regnamespace`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec(`CREATE SCHEMA family_definition_dest`, 0))
		run(fixture.Prepared(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree RENAME TO family_renamed`, 0)))
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_renamed USING btree SET SCHEMA family_definition_dest`, 0))
		run(fixture.Select(`SELECT opfmethod,opfname FROM pg_opfamily WHERE opfname='family_renamed' AND opfnamespace='family_definition_dest'::regnamespace`, []string{"opfmethod", "opfname"}, []string{"OID", "NAME"}, [][]any{{int64(403), "family_renamed"}}))
		// Dynamic identities are relational assertions, never guessed OID goldens.
		var after int64
		if err := conn.QueryRowContext(ctx, `SELECT oid FROM pg_opfamily WHERE opfname='family_renamed' AND opfnamespace='family_definition_dest'::regnamespace`).Scan(&after); err != nil {
			t.Fatal(err)
		}
		if after != before {
			t.Fatalf("rename/move changed identity: %d -> %d", before, after)
		}
		run(fixture.Exec(`ALTER OPERATOR FAMILY family_definition_dest.family_renamed USING btree DROP OPERATOR 1(integer), FUNCTION 1(integer)`, 0))
		run(fixture.Exec(`DROP OPERATOR FAMILY family_definition_dest.family_renamed USING btree`, 0))
		run(fixture.Select(`SELECT opfmethod FROM pg_opfamily WHERE opfname='family_probe' AND opfnamespace='public'::regnamespace`, []string{"opfmethod"}, []string{"OID"}, [][]any{{int64(405)}}))
		run(fixture.Exec(`CREATE OPERATOR family_definition_dest.=== (LEFTARG=integer, RIGHTARG=integer, FUNCTION=pg_catalog.int4eq)`, 0))
		run(fixture.Exec(`SET search_path=family_definition_dest,pg_catalog`, 0))
		run(fixture.Exec(`CREATE OPERATOR FAMILY family_path USING btree`, 0))
		run(fixture.Prepared(fixture.Exec(`ALTER OPERATOR FAMILY family_path USING btree ADD OPERATOR 3 ===(integer,integer), FUNCTION 1 btint4cmp(integer,integer)`, 0)))
		run(fixture.Select(`SELECT o.oprname,p.amproc::oid::bigint FROM pg_opfamily f JOIN pg_amop a ON a.amopfamily=f.oid JOIN pg_operator o ON o.oid=a.amopopr JOIN pg_amproc p ON p.amprocfamily=f.oid WHERE f.opfname='family_path' AND f.opfnamespace='family_definition_dest'::regnamespace`, []string{"oprname", "amproc"}, []string{"NAME", "INT8"}, [][]any{{"===", int64(351)}}))
		run(fixture.Exec(`RESET search_path`, 0))
	})
}

func TestReferenceOperatorFamilyMetadata(t *testing.T) {
	familyScenario(t, func(t *testing.T, _ context.Context, _ *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec(familyCreate, 0))
		// Empty producers still expose the complete released descriptors.
		run(fixture.Select(`SELECT oid,opfmethod,opfname,opfnamespace,opfowner FROM pg_opfamily WHERE opfname='family_never_exists'`, []string{"oid", "opfmethod", "opfname", "opfnamespace", "opfowner"}, []string{"OID", "OID", "NAME", "OID", "OID"}, nil))
		run(fixture.Select(`SELECT oid,amopfamily,amoplefttype,amoprighttype,amopstrategy,amoppurpose,amopopr,amopmethod,amopsortfamily FROM pg_amop WHERE amopfamily=0`, []string{"oid", "amopfamily", "amoplefttype", "amoprighttype", "amopstrategy", "amoppurpose", "amopopr", "amopmethod", "amopsortfamily"}, []string{"OID", "OID", "OID", "OID", "INT2", "CHAR", "OID", "OID", "OID"}, nil))
		run(fixture.Select(`SELECT oid,amprocfamily,amproclefttype,amprocrighttype,amprocnum,amproc FROM pg_amproc WHERE amprocfamily=0`, []string{"oid", "amprocfamily", "amproclefttype", "amprocrighttype", "amprocnum", "amproc"}, []string{"OID", "OID", "OID", "OID", "INT2", "REGPROC"}, nil))
		run(fixture.Exec(familyPopulate, 0))
		run(familyOperatorStep())
		run(fixture.Select(`SELECT f.opfmethod,f.opfname,n.nspname,r.rolname=current_user AS owned FROM pg_opfamily f JOIN pg_namespace n ON n.oid=f.opfnamespace JOIN pg_roles r ON r.oid=f.opfowner WHERE f.opfname='family_probe' AND n.nspname='public'`, []string{"opfmethod", "opfname", "nspname", "owned"}, []string{"OID", "NAME", "NAME", "BOOL"}, [][]any{{int64(403), "family_probe", "public", true}}))
		run(fixture.Select(`SELECT count(*),count(DISTINCT a.oid) FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace AND a.oid<>0`, []string{"count", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(5), int64(5)}}))
		run(fixture.Select(`SELECT p.amproclefttype,p.amprocrighttype,p.amprocnum,p.amproc::oid FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace`, []string{"amproclefttype", "amprocrighttype", "amprocnum", "amproc"}, []string{"OID", "OID", "INT2", "OID"}, [][]any{{int64(23), int64(23), int64(1), int64(351)}}))
	})
}

func TestReferenceOperatorFamilyAtomicity(t *testing.T) {
	familyScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec(familyCreate, 0))
		run(fixture.Exec(familyPopulate, 0))
		identity := func() string {
			var text string
			if err := conn.QueryRowContext(ctx, `SELECT oid::text FROM pg_opfamily WHERE opfname='family_probe' AND opfnamespace='public'::regnamespace`).Scan(&text); err != nil {
				t.Fatal(err)
			}
			return text
		}
		before := identity()
		cases := []struct{ sql, code, message string }{
			{`ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal), FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, "42710", `function 1(integer,integer) already exists in operator family "public.family_probe"`},
			{`ALTER OPERATOR FAMILY public.family_probe USING btree DROP OPERATOR 1(integer), OPERATOR 1(bigint)`, "42704", `operator 1(bigint,bigint) does not exist in operator family "public.family_probe"`},
			{`ALTER OPERATOR FAMILY public.family_probe USING btree DROP FUNCTION 1(integer), FUNCTION 2(integer)`, "42704", `function 2(integer,integer) does not exist in operator family "public.family_probe"`},
			{`ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal), FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`, "42P17", `function number 2 for (integer,integer) appears more than once`},
		}
		for _, tc := range cases {
			run(fixture.ImplicitError(tc.sql, fixture.Diagnostic{Code: tc.code, Message: tc.message}))
			run(familyOperatorStep())
			run(fixture.Select(`SELECT p.amprocnum,p.amproc::oid FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace`, []string{"amprocnum", "amproc"}, []string{"INT2", "OID"}, [][]any{{int64(1), int64(351)}}))
			if got := identity(); got != before {
				t.Fatalf("failed mutation changed family identity %s -> %s", before, got)
			}
			run(fixture.Select(`SELECT 42 AS sentinel`, []string{"sentinel"}, []string{"INT4"}, [][]any{{int64(42)}}))
		}
	})
}

func TestReferenceOperatorFamilyDependencies(t *testing.T) {
	familyScenario(t, func(t *testing.T, _ context.Context, _ *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec(familyCreate, 0))
		run(fixture.Exec(`CREATE OPERATOR public.=== (LEFTARG=integer, RIGHTARG=integer, FUNCTION=pg_catalog.int4eq)`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 3 public.===(integer,integer), FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, 0))
		// A loose membership is AUTO, not an invented RESTRICT dependency.
		run(fixture.Exec(`DROP OPERATOR public.===(integer,integer)`, 0))
		run(fixture.Select(`SELECT count(*) FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select(`SELECT count(*) FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='family_probe' AND f.opfnamespace='public'::regnamespace`, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(fixture.Exec(`CREATE OPERATOR public.=== (LEFTARG=integer, RIGHTARG=integer, FUNCTION=pg_catalog.int4eq)`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 3 public.===(integer,integer)`, 0))
		run(fixture.Exec(`DROP OPERATOR FAMILY public.family_probe USING btree RESTRICT`, 0))
		run(fixture.Select(`SELECT pg_catalog.btint4cmp(1,3) AS comparison`, []string{"comparison"}, []string{"INT4"}, [][]any{{int64(-1)}}))
		run(fixture.Select(`SELECT count(*) FROM pg_operator WHERE oprname='===' AND oprnamespace='public'::regnamespace AND oprleft=23 AND oprright=23`, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}
