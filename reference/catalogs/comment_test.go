//go:build reference

package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCommentRoundtrip(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			role := "comment_roundtrip_" + fmt.Sprint(prepared)
			fixture.RoleReferenceCleanup(t, []string{role})
			p := fixture.NewPair(t)
			step := func(q string) { s := fixture.Exec(q, 0); s.Prepared = prepared; p.Step(t, s) }
			step("CREATE ROLE " + role)
			step(`CREATE TABLE "t.dot"(a INT,b TEXT)`)
			var nativeSchemaOID, oracleSchemaOID, oracleRoleOID, oracleBootstrapOID int64
			for _, backend := range []struct {
				db  *sql.DB
				oid *int64
			}{
				{p.Memory, &nativeSchemaOID}, {p.Postgres, &oracleSchemaOID},
			} {
				if err := backend.db.QueryRowContext(p.Ctx, "SELECT relnamespace FROM pg_class WHERE relname='t.dot'").Scan(backend.oid); err != nil {
					t.Fatal(err)
				}
			}
			if err := p.Postgres.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&oracleRoleOID); err != nil {
				t.Fatal(err)
			}
			if err := p.Postgres.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='postgres'").Scan(&oracleBootstrapOID); err != nil {
				t.Fatal(err)
			}
			// Only bootstrap10 is a fixed native role address. Named role
			// identity and reuse are witnessed through actual RoleRead internally.
			targets := []struct{ target, native, oracle string }{
				{`TABLE "t.dot"`, `SELECT obj_description('public."t.dot"'::regclass,'pg_class')`, `SELECT obj_description('public."t.dot"'::regclass,'pg_class')`},
				{`COLUMN "t.dot".a`, `SELECT col_description('public."t.dot"'::regclass,1)`, `SELECT col_description('public."t.dot"'::regclass,1)`},
				{`COLUMN "t.dot".b`, `SELECT col_description('public."t.dot"'::regclass,2)`, `SELECT col_description('public."t.dot"'::regclass,2)`},
				{`SCHEMA public`, fmt.Sprintf("SELECT obj_description(%d,'pg_namespace')", nativeSchemaOID), fmt.Sprintf("SELECT obj_description(%d,'pg_namespace')", oracleSchemaOID)},
				{`ROLE postgres`, `SELECT shobj_description(10,'pg_authid')`, fmt.Sprintf("SELECT shobj_description(%d,'pg_authid')", oracleBootstrapOID)},
			}
			// Values are release expectations, not derived from the other backend.
			values := []struct {
				sql      string
				expected sql.NullString
			}{{`E'quote\' slash\\\n雪'`, sql.NullString{String: "quote' slash\\\n雪", Valid: true}}, {`'replacement'`, sql.NullString{String: "replacement", Valid: true}}, {`NULL`, sql.NullString{}}, {`''`, sql.NullString{}}, {`' '`, sql.NullString{String: " ", Valid: true}}}
			for _, target := range targets {
				for _, value := range values {
					step("COMMENT ON " + target.target + " IS " + value.sql)
					for _, backend := range []struct {
						name, query string
						db          *sql.DB
					}{
						{"native", target.native, p.Memory}, {"oracle", target.oracle, p.Postgres},
					} {
						var observed sql.NullString
						if err := backend.db.QueryRowContext(p.Ctx, backend.query).Scan(&observed); err != nil || observed != value.expected {
							t.Fatalf("%s metadata %s: %#v want %#v: %v", backend.name, target.target, observed, value.expected, err)
						}
					}
				}
			}
			for _, value := range values {
				step("COMMENT ON ROLE " + role + " IS " + value.sql)
				var observed sql.NullString
				if err := p.Postgres.QueryRowContext(p.Ctx, "SELECT shobj_description($1,'pg_authid')", oracleRoleOID).Scan(&observed); err != nil || observed != value.expected {
					t.Fatal("named oracle role lifecycle", observed, value.expected, err)
				}
			}
			step("COMMENT ON ROLE " + role + " IS 'drop dependency must not exist'")
			step("DROP ROLE " + role)
			step("CREATE ROLE " + role)
			var absent sql.NullString
			if err := p.Postgres.QueryRowContext(p.Ctx, `SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname='`+role+`'`).Scan(&absent); err != nil || absent.Valid {
				t.Fatal("shared name reuse leaked metadata", absent, err)
			}
			var recreatedRoleOID int64
			if err := p.Postgres.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname=$1", role).Scan(&recreatedRoleOID); err != nil || recreatedRoleOID == oracleRoleOID {
				t.Fatal("oracle role identity reused", recreatedRoleOID, oracleRoleOID, err)
			}
			step("COMMENT ON ROLE " + role + " IS 'new identity'")
			if err := p.Postgres.QueryRowContext(p.Ctx, "SELECT shobj_description($1,'pg_authid')", recreatedRoleOID).Scan(&absent); err != nil || !absent.Valid || absent.String != "new identity" {
				t.Fatal("recreated oracle role positive witness", absent, err)
			}
			if err := p.Postgres.QueryRowContext(p.Ctx, "SELECT shobj_description($1,'pg_authid')", oracleRoleOID).Scan(&absent); err != nil || absent.Valid {
				t.Fatal("dropped oracle role retained comment", absent, err)
			}
			step(`DROP TABLE "t.dot"`)
			step(`CREATE TABLE "t.dot"(a INT,b TEXT)`)
			if err := p.Postgres.QueryRowContext(p.Ctx, targets[0].oracle).Scan(&absent); err != nil || absent.Valid {
				t.Fatal("local name reuse leaked metadata", absent, err)
			}
			if err := p.Memory.QueryRowContext(p.Ctx, targets[0].native).Scan(&absent); err != nil || absent.Valid {
				t.Fatal("native local name reuse leaked metadata", absent, err)
			}
			step("CREATE SCHEMA comment_scope CREATE TABLE t(v INT)")
			step("COMMENT ON SCHEMA comment_scope IS 'schema lifecycle'")
			step("COMMENT ON TABLE comment_scope.t IS 'table lifecycle'")
			step("COMMENT ON COLUMN comment_scope.t.v IS 'column lifecycle'")
			step("ALTER SCHEMA comment_scope RENAME TO comment_renamed")
			for _, probe := range []struct{ q, want string }{
				{`SELECT obj_description('comment_renamed'::regnamespace,'pg_namespace')`, "schema lifecycle"},
				{`SELECT obj_description('comment_renamed.t'::regclass,'pg_class')`, "table lifecycle"},
				{`SELECT col_description('comment_renamed.t'::regclass,1)`, "column lifecycle"},
			} {
				var got string
				if err := p.Postgres.QueryRowContext(p.Ctx, probe.q).Scan(&got); err != nil || got != probe.want {
					t.Fatal("rename lost comment", got, probe.want, err)
				}
			}
			step("DROP SCHEMA comment_renamed CASCADE")
			step("CREATE SCHEMA comment_renamed CREATE TABLE t(v INT)")
			if err := p.Postgres.QueryRowContext(p.Ctx, `SELECT obj_description('comment_renamed'::regnamespace,'pg_namespace')`).Scan(&absent); err != nil || absent.Valid {
				t.Fatal("schema name reuse leaked metadata", absent, err)
			}
			step("DROP SCHEMA comment_renamed CASCADE")
		})
	}
}

func TestReferenceCommentLookup(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewPair(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(a INT)", 0))
			for _, tc := range []struct{ q, code, message string }{
				{"COMMENT ON TABLE missing IS NULL", "42P01", `relation "missing" does not exist`},
				{"COMMENT ON TABLE public.missing IS NULL", "42P01", `relation "public.missing" does not exist`},
				{"COMMENT ON TABLE missing_schema.t IS NULL", "3F000", `schema "missing_schema" does not exist`},
				{"COMMENT ON COLUMN missing_schema.t.v IS NULL", "3F000", `schema "missing_schema" does not exist`},
				{"COMMENT ON COLUMN t.missing IS NULL", "42703", `column "missing" of relation "t" does not exist`},
				{"COMMENT ON COLUMN public.t.missing IS NULL", "42703", `column "missing" of relation "public.t" does not exist`},
				{"COMMENT ON COLUMN a IS NULL", "42601", "column name must be qualified"},
				{"COMMENT ON SCHEMA missing IS NULL", "3F000", `schema "missing" does not exist`},
				{"COMMENT ON ROLE missing IS NULL", "42704", `role "missing" does not exist`},
				{"COMMENT ON ROLE public IS NULL", "42704", `role "public" does not exist`},
			} {
				p.Step(t, fixture.RoleReferenceError(tc.q, tc.code, tc.message, "", prepared, "execute"))
			}
			invalid := fixture.RoleReferenceError("COMMENT ON TABLE t IS 7", "42601", `syntax error at or near "7"`, "", prepared, "prepare")
			invalid.Diagnostic.Position = "23"
			p.Step(t, invalid)
		})
	}
}

func TestReferenceCommentAuthorization(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			owner := "comment_owner_" + fmt.Sprint(prepared)
			other := "comment_other_" + fmt.Sprint(prepared)
			fixture.RoleReferenceCleanup(t, []string{owner, other})
			p := fixture.NewPair(t)
			for _, name := range []string{owner, other} {
				p.Step(t, fixture.Exec("CREATE USER "+name, 0))
			}
			if _, err := fixture.Admin.ExecContext(p.Ctx, "GRANT USAGE, CREATE ON SCHEMA public TO PUBLIC"); err != nil {
				t.Fatal(err)
			}
			for _, backend := range []struct {
				name, bootstrap string
				db              *sql.DB
				record          *fixture.Recorder
			}{{"memory", "postgres", p.Memory, p.MemoryRecord}, {"postgres", "stuntgres", p.Postgres, p.PostgresRecord}} {
				t.Run(backend.name, func(t *testing.T) {
					conn, err := backend.db.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.record, s) }
					run(fixture.Exec("CREATE SCHEMA comment_owned AUTHORIZATION "+owner, 0))
					run(fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
					run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					run(fixture.Exec("COMMENT ON TABLE t IS 'old'", 0))
					held, err := conn.PrepareContext(p.Ctx, "COMMENT ON TABLE t IS 'held'")
					if err != nil {
						t.Fatal("valid utility protocol Prepare", err)
					}
					defer held.Close()
					run(fixture.Exec("SET SESSION AUTHORIZATION "+other, 0))
					for _, tc := range []struct{ q, code, message, detail string }{
						{"COMMENT ON TABLE t IS 'bad'", "42501", "must be owner of table t", ""},
						{"COMMENT ON COLUMN t.a IS 'bad'", "42501", "must be owner of relation t", ""},
						{"COMMENT ON SCHEMA comment_owned IS 'bad'", "42501", "must be owner of schema comment_owned", ""},
						{"COMMENT ON COLUMN t.missing IS 'bad'", "42703", `column "missing" of relation "t" does not exist`, ""},
						{"COMMENT ON ROLE " + other + " IS 'bad'", "42501", "permission denied", "The current user must have the CREATEROLE attribute."},
						{"COMMENT ON ROLE " + backend.bootstrap + " IS 'bad'", "42501", "permission denied", "The current user must have the SUPERUSER attribute."},
					} {
						run(fixture.RoleReferenceError(tc.q, tc.code, tc.message, tc.detail, prepared, "execute"))
					}
					_, err = held.ExecContext(p.Ctx)
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"})
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Exec("DROP TABLE t", 0))
					run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					run(fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
					_, err = held.ExecContext(p.Ctx)
					fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "must be owner of table t"})
					run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
					run(fixture.Exec("DROP TABLE t", 0))
					run(fixture.Exec("DROP SCHEMA comment_owned", 0))
				})
			}
		})
	}
}
