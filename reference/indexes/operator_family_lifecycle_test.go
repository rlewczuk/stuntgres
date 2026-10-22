package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceOperatorFamilyMetadataInvalidation(t *testing.T) {
	familyScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		run(fixture.Exec(familyCreate, 0))
		run(fixture.Exec(familyPopulate, 0))
		const q = `SELECT f.opfname,a.oid::bigint,a.amopopr::bigint FROM pg_opfamily f JOIN pg_amop a ON a.amopfamily=f.oid WHERE f.opfnamespace='public'::regnamespace AND f.opfmethod=403 AND a.amopstrategy=1`
		stmt, err := conn.PrepareContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		read := func(name string, present bool) int64 {
			rows, err := stmt.QueryContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			names := []string{"opfname", "oid", "amopopr"}
			types := []string{"NAME", "INT8", "INT8"}
			if len(columns) != 3 {
				t.Fatal(columns)
			}
			for i, c := range columns {
				if c.Name() != names[i] || c.DatabaseTypeName() != types[i] {
					t.Fatalf("column %d: %v", i, c)
				}
			}
			var oid int64
			if rows.Next() {
				var nameGot string
				var operator int64
				if err := rows.Scan(&nameGot, &oid, &operator); err != nil {
					t.Fatal(err)
				}
				if !present || nameGot != name || operator != 97 || oid == 0 {
					t.Fatal(nameGot, oid, operator)
				}
				if rows.Next() {
					t.Fatal("extra member")
				}
			} else if present {
				t.Fatal("missing member")
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			return oid
		}
		before := read("family_probe", true)
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree DROP OPERATOR 1(integer)`, 0))
		read("family_probe", false)
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 1 pg_catalog.<(integer,integer)`, 0))
		after := read("family_probe", true)
		if after == before {
			t.Fatal("readded operator reused member identity")
		}
		run(fixture.ImplicitError(`ALTER OPERATOR FAMILY public.family_probe USING btree ADD OPERATOR 1 pg_catalog.<(integer,integer)`, fixture.Diagnostic{Code: "42710", Message: `operator 1(integer,integer) already exists in operator family "public.family_probe"`}))
		if read("family_probe", true) != after {
			t.Fatal("failed edit changed identity")
		}
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree RENAME TO family_metadata_renamed`, 0))
		if read("family_metadata_renamed", true) != after {
			t.Fatal("rename changed member identity")
		}
		const supportSQL = `SELECT p.amproc::text FROM pg_amproc p JOIN pg_opfamily f ON f.oid=p.amprocfamily WHERE f.opfname='family_metadata_renamed' AND f.opfnamespace='public'::regnamespace`
		support, err := conn.PrepareContext(ctx, supportSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer support.Close()
		supportName := func(want string) {
			rows, err := support.QueryContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if !rows.Next() {
				t.Fatal("missing held support", rows.Err())
			}
			var got string
			if err := rows.Scan(&got); err != nil || got != want {
				t.Fatal(got, err)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra held support", rows.Err())
			}
		}
		supportName("btint4cmp")
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_metadata_renamed USING btree DROP FUNCTION 1(integer)`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_metadata_renamed USING btree ADD FUNCTION 1 pg_catalog.int4mi(integer,integer)`, 0))
		supportName("int4mi")
	})
}

func TestReferenceOperatorFamilyNotices(t *testing.T) {
	familyBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		var notices []fixture.Diagnostic
		_, conn, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, prepared := range []bool{false, true} {
			notices = nil
			s := fixture.Exec(`DROP OPERATOR FAMILY IF EXISTS public.family_notice_absent USING btree`, 0)
			s.Prepared = prepared
			fixture.RunStep(t, p.Ctx, conn, rec, s)
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `operator family "public.family_notice_absent" does not exist for access method "btree", skipping`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatalf("notices: %+v; want %+v", notices, want)
			}
		}
	})
}

func TestReferenceOperatorFamilyAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"family_authority_reader", "family_authority_dest"})
	fixture.SchemaReferenceCleanup(t, "family_authority_schema")
	fixture.SchemaReferenceCleanup(t, "family_authority_denied_schema")
	familyScenario(t, func(t *testing.T, ctx context.Context, conn *sql.Conn, run func(fixture.Step)) {
		defer conn.ExecContext(ctx, `RESET ROLE`)
		run(fixture.Exec(familyCreate, 0))
		run(fixture.Exec(familyPopulate, 0))
		run(fixture.Exec(`CREATE ROLE family_authority_reader`, 0))
		run(fixture.Exec(`CREATE ROLE family_authority_dest`, 0))
		run(fixture.Exec(`GRANT USAGE,CREATE ON SCHEMA public TO family_authority_reader`, 0))
		run(fixture.Exec(`CREATE SCHEMA family_authority_denied_schema`, 0))
		const heldSQL = `ALTER OPERATOR FAMILY public.family_probe USING btree DROP FUNCTION 1(integer)`
		held, err := conn.PrepareContext(ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		run(fixture.Exec(`SET ROLE family_authority_reader`, 0))
		cases := []struct{ q, code, message string }{
			{`CREATE OPERATOR FAMILY public.family_denied USING btree`, "42501", `must be superuser to create an operator family`},
			{`ALTER OPERATOR FAMILY public.family_probe USING btree ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`, "42501", `must be superuser to alter an operator family`},
			{`ALTER OPERATOR FAMILY public.family_absent USING family_missing ADD FUNCTION 1 pg_catalog.abs(integer)`, "42704", `access method "family_missing" does not exist`},
			{`ALTER OPERATOR FAMILY public.family_absent USING btree ADD FUNCTION 1 pg_catalog.abs(integer)`, "42704", `operator family "public.family_absent" does not exist for access method "btree"`},
			{`DROP OPERATOR FAMILY public.family_probe USING btree`, "42501", `must be owner of operator family btree.public.family_probe`},
			{`ALTER OPERATOR FAMILY public.family_probe USING btree RENAME TO family_denied`, "42501", `must be owner of operator family family_probe`},
		}
		for _, tc := range cases {
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: tc.code, Message: tc.message})
				s.Prepared = prepared
				run(s)
			}
		}
		_, err = held.ExecContext(ctx)
		want := fixture.ControlsReferenceDiagnostic("42501", `must be superuser to alter an operator family`)
		fixture.AssertDiagnostic(t, err, &want)
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree OWNER TO family_authority_reader`, 0))
		run(fixture.Exec(`SET ROLE family_authority_reader`, 0))
		run(fixture.ImplicitError(heldSQL, fixture.Diagnostic{Code: "42501", Message: `must be superuser to alter an operator family`}))
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_probe USING btree RENAME TO family_owner_renamed`, 0))
		run(fixture.ImplicitError(`ALTER OPERATOR FAMILY public.family_owner_renamed USING btree SET SCHEMA family_authority_denied_schema`, fixture.Diagnostic{Code: "42501", Message: `permission denied for schema family_authority_denied_schema`}))
		run(fixture.ImplicitError(`ALTER OPERATOR FAMILY public.family_owner_renamed USING btree OWNER TO family_authority_dest`, fixture.Diagnostic{Code: "42501", Message: `must be able to SET ROLE "family_authority_dest"`}))
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec(`CREATE SCHEMA family_authority_schema AUTHORIZATION family_authority_reader`, 0))
		run(fixture.Exec(`SET ROLE family_authority_reader`, 0))
		run(fixture.Exec(`ALTER OPERATOR FAMILY public.family_owner_renamed USING btree SET SCHEMA family_authority_schema`, 0))
		run(fixture.Exec(`RESET ROLE`, 0))
		run(fixture.Exec(`REASSIGN OWNED BY family_authority_reader TO family_authority_dest`, 0))
		run(fixture.Select(`SELECT f.opfname,r.rolname,n.nspname FROM pg_opfamily f JOIN pg_roles r ON r.oid=f.opfowner JOIN pg_namespace n ON n.oid=f.opfnamespace WHERE f.opfname='family_owner_renamed'`, []string{"opfname", "rolname", "nspname"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"family_owner_renamed", "family_authority_dest", "family_authority_schema"}}))
		run(fixture.Exec(`DROP OWNED BY family_authority_dest CASCADE`, 0))
		run(fixture.Select(`SELECT count(*) FROM pg_opfamily WHERE opfname='family_owner_renamed'`, []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	})
}

func TestReferenceOperatorFamilyTransactionBoundaries(t *testing.T) {
	familyBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(fixture.Exec(`CREATE OPERATOR FAMILY public.family_boundary USING btree`, 0))
		for _, tc := range []struct{ query, tag string }{
			{familyCreate, "CREATE OPERATOR FAMILY"},
			{`ALTER OPERATOR FAMILY public.family_boundary USING btree RENAME TO family_boundary_renamed`, "ALTER OPERATOR FAMILY"},
			{`DROP OPERATOR FAMILY public.family_boundary USING btree`, "DROP OPERATOR FAMILY"},
		} {
			for _, prepared := range []bool{false, true} {
				run(fixture.Exec(`BEGIN READ ONLY`, 0))
				s := fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "25006", Message: `cannot execute ` + tc.tag + ` in a read-only transaction`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Exec(`ROLLBACK`, 0))
				run(fixture.Exec(`BEGIN`, 0))
				s = fixture.Exec(tc.query, 0)
				if !b.PG {
					s = fixture.ImplicitError(tc.query, fixture.Diagnostic{Code: "0A000", Message: `statement is unsupported in an explicit transaction`})
					s.ErrorStage = "execute"
				}
				s.Prepared = prepared
				run(s)
				run(fixture.Exec(`ROLLBACK`, 0))
			}
		}
		run(fixture.Exec(`DROP OPERATOR FAMILY public.family_boundary USING btree`, 0))
	})
}
