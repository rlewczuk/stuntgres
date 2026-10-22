package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceOperatorClassLifecycle(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"class_life_owner", "class_life_other"})
	fixture.SchemaReferenceCleanup(t, "class_life_dest", "class_life_denied")
	fixture.SchemaReferenceCleanup(t, "class_graph_schema")
	fixture.SchemaReferenceCleanup(t, "class_mutation_schema")
	classBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, run := classConn(t, p, b)
		defer conn.ExecContext(p.Ctx, `RESET ROLE`)
		for _, q := range []string{
			`CREATE ROLE class_life_owner`, `CREATE ROLE class_life_other`,
			`CREATE SCHEMA class_life_dest AUTHORIZATION class_life_owner`, `CREATE SCHEMA class_life_denied`,
			`GRANT USAGE,CREATE ON SCHEMA public TO class_life_owner`,
			`CREATE OPERATOR FAMILY public.class_life_family USING btree`,
			classCreate("public.class_life_old", "public.class_life_family", "btint4cmp"),
			`ALTER OPERATOR FAMILY public.class_life_family USING btree ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal), FUNCTION 1 pg_catalog.btint48cmp(integer,bigint)`,
			`CREATE TABLE class_life_rows(n integer)`, `INSERT INTO class_life_rows VALUES(2),(1)`,
			`CREATE INDEX class_life_index ON class_life_rows(n public.class_life_old)`,
		} {
			count := int64(0)
			if q == `INSERT INTO class_life_rows VALUES(2),(1)` {
				count = 2
			}
			run(fixture.Exec(q, count))
		}
		var oid int64
		if err := conn.QueryRowContext(p.Ctx, `SELECT oid::bigint FROM pg_opclass WHERE opcname='class_life_old'`).Scan(&oid); err != nil {
			t.Fatal(err)
		}
		const metadata = `SELECT c.opcname,n.nspname,r.rolname,c.opcmethod::bigint,c.opcintype::bigint,c.opcdefault,c.opckeytype::bigint,f.opfname,fn.nspname FROM pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace JOIN pg_roles r ON r.oid=c.opcowner JOIN pg_opfamily f ON f.oid=c.opcfamily JOIN pg_namespace fn ON fn.oid=f.opfnamespace WHERE c.oid=$1::oid`
		mh := classHeld(t, p, conn, metadata)
		const data = `SELECT n FROM class_life_rows WHERE n >= $1 ORDER BY n`
		dh := classHeld(t, p, conn, data)
		check := func(name, schema, owner string) {
			s := fixture.Select(metadata, []string{"opcname", "nspname", "rolname", "opcmethod", "opcintype", "opcdefault", "opckeytype", "opfname", "nspname"}, []string{"NAME", "NAME", "NAME", "INT8", "INT8", "BOOL", "INT8", "NAME", "NAME"}, [][]any{{name, schema, owner, int64(403), int64(23), false, int64(0), "class_life_family", "public"}}, oid)
			classRoutes(t, p, b, conn, s)
			fixture.RunStep(t, p.Ctx, mh, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, dh, b.Recorder, fixture.Select(data, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, 1))
		}
		var owner string
		if err := conn.QueryRowContext(p.Ctx, `SELECT r.rolname FROM pg_roles r JOIN pg_opclass c ON c.opcowner=r.oid WHERE c.oid=$1::oid`, oid).Scan(&owner); err != nil {
			t.Fatal(err)
		}
		check("class_life_old", "public", owner)
		retained, err := conn.QueryContext(p.Ctx, `SELECT opcname FROM pg_opclass WHERE oid=$1::oid`, oid)
		if err != nil {
			t.Fatal(err)
		}
		// Use another session while a real Rows owns its original connection.
		runOther := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		runOther(fixture.Exec(`ALTER OPERATOR CLASS public.class_life_old USING btree RENAME TO class_life_current`, 0))
		if !retained.Next() {
			t.Fatal(retained.Err())
		}
		var old string
		if err := retained.Scan(&old); err != nil || old != "class_life_old" {
			t.Fatal(old, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained rows", retained.Err())
		}
		retained.Close()
		run(fixture.Exec(`ALTER OPERATOR CLASS public.class_life_current USING btree SET SCHEMA class_life_dest`, 0))
		run(fixture.Exec(`ALTER OPERATOR CLASS class_life_dest.class_life_current USING btree OWNER TO class_life_owner`, 0))
		check("class_life_current", "class_life_dest", "class_life_owner")
		t.Run("authority", func(t *testing.T) {
			r := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			r(fixture.Exec(`SET ROLE class_life_other`, 0))
			defer conn.ExecContext(p.Ctx, `RESET ROLE`)
			cases := []struct{ q, message string }{
				{`ALTER OPERATOR CLASS class_life_dest.class_life_current USING btree RENAME TO class_life_denied`, `permission denied for schema class_life_dest`},
				{`CREATE OPERATOR CLASS public.class_life_denied FOR TYPE integer USING btree AS STORAGE integer`, `permission denied for schema public`},
			}
			for _, tc := range cases {
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42501", Message: tc.message})
					s.Prepared, s.ErrorStage = prepared, "execute"
					r(s)
				}
			}
			r(fixture.Exec(`RESET ROLE`, 0))
			r(fixture.Exec(`SET ROLE class_life_owner`, 0))
			for _, tc := range []struct{ q, message string }{
				{`CREATE OPERATOR CLASS public.class_life_denied FOR TYPE integer USING btree AS STORAGE integer`, `must be superuser to create an operator class`},
				{`CREATE OPERATOR CLASS public.class_life_denied FOR TYPE integer USING class_life_missing AS STORAGE integer`, `access method "class_life_missing" does not exist`},
				{`ALTER OPERATOR CLASS class_life_dest.class_life_current USING btree SET SCHEMA class_life_denied`, `permission denied for schema class_life_denied`},
				{`ALTER OPERATOR CLASS class_life_dest.class_life_current USING btree OWNER TO class_life_other`, `must be able to SET ROLE "class_life_other"`},
			} {
				code := "42501"
				if tc.message == `access method "class_life_missing" does not exist` {
					code = "42704"
				}
				for _, prepared := range []bool{false, true} {
					s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: code, Message: tc.message})
					s.Prepared, s.ErrorStage = prepared, "execute"
					r(s)
				}
			}
		})
		run(fixture.Exec(`RESET ROLE`, 0))
		check("class_life_current", "class_life_dest", "class_life_owner")
		t.Run("restrict", func(t *testing.T) {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(`DROP OPERATOR CLASS class_life_dest.class_life_current USING btree`, fixture.Diagnostic{Code: "2BP01", Message: `cannot drop operator class class_life_dest.class_life_current for access method btree because other objects depend on it`, Detail: `index class_life_index depends on operator class class_life_dest.class_life_current for access method btree`, Hint: `Use DROP ... CASCADE to drop the dependent objects too.`})
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				check("class_life_current", "class_life_dest", "class_life_owner")
			}
		})
		run(fixture.Exec(`DROP OPERATOR CLASS class_life_dest.class_life_current USING btree CASCADE`, 0))
		run(fixture.Select(`SELECT relname FROM pg_class WHERE relname='class_life_index'`, []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Select(`SELECT opcname FROM pg_opclass WHERE oid=$1::oid`, []string{"opcname"}, []string{"NAME"}, nil, oid))
		run(fixture.Select(`SELECT opfname FROM pg_opfamily WHERE opfname='class_life_family'`, []string{"opfname"}, []string{"NAME"}, [][]any{{"class_life_family"}}))
		run(fixture.Select(`SELECT p.amprocnum,p.amproclefttype::bigint,p.amprocrighttype::bigint FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='class_life_family' ORDER BY p.amprocnum,p.amprocrighttype`, []string{"amprocnum", "amproclefttype", "amprocrighttype"}, []string{"INT2", "INT8", "INT8"}, [][]any{{int64(1), int64(23), int64(20)}, {int64(2), int64(23), int64(23)}}))
		run(fixture.Select(`SELECT a.amopstrategy FROM pg_amop a JOIN pg_opfamily f ON f.oid=a.amopfamily WHERE f.opfname='class_life_family'`, []string{"amopstrategy"}, []string{"INT2"}, nil))
		run(fixture.Select(`SELECT oid::bigint FROM pg_proc WHERE oid=351`, []string{"oid"}, []string{"INT8"}, [][]any{{int64(351)}}))
		run(fixture.Select(`SELECT oid::bigint FROM pg_operator WHERE oid=97`, []string{"oid"}, []string{"INT8"}, [][]any{{int64(97)}}))
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, prepared := range []bool{false, true} {
			notices = nil
			s := fixture.Exec(`DROP OPERATOR CLASS IF EXISTS public.class_life_absent USING btree`, 0)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, nc, nr, s)
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `operator class "public.class_life_absent" does not exist for access method "btree", skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("notices %#v; want %#v", notices, want)
			}
		}
		for _, q := range []string{
			`CREATE OPERATOR CLASS class_life_dest.class_life_owned FOR TYPE integer USING btree AS FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`,
			`ALTER OPERATOR CLASS class_life_dest.class_life_owned USING btree OWNER TO class_life_owner`,
			`REASSIGN OWNED BY class_life_owner TO class_life_other`,
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Select(`SELECT r.rolname FROM pg_opclass c JOIN pg_roles r ON r.oid=c.opcowner WHERE c.opcname='class_life_owned'`, []string{"rolname"}, []string{"NAME"}, [][]any{{"class_life_other"}}))
		run(fixture.Exec(`DROP OWNED BY class_life_other CASCADE`, 0))
		run(fixture.Select(`SELECT opcname FROM pg_opclass WHERE opcname='class_life_owned'`, []string{"opcname"}, []string{"NAME"}, nil))
		classGraphBoundaries(t, p, b, conn)
		classMutationBoundaries(t, p, b, conn)
	})
}
