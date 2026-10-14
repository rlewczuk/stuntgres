package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceSmokeCommentSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"mixed_table_owner", "mixed_schema_owner", "owner", "other"})
	fixture.SchemaReferenceCleanup(t, "s.dot", "renamed", "mixed", "owned")
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := p.Ctx
			db := backend.DB
			m := fixture.MigrationSmoke{T: t, PG: backend.PG, Native: p.Native, Recorder: backend.Recorder}
			a, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := db.Conn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			comment := func(q, label string, want any) {
				t.Helper()
				fixture.RunStep(t, ctx, a, backend.Recorder, fixture.Select(q, []string{label}, []string{"TEXT"}, [][]any{{want}}))
			}
			const tableComment = `SELECT obj_description('"s.dot"."t.dot"'::regclass,'pg_class')`
			const firstComment = `SELECT col_description('"s.dot"."t.dot"'::regclass,1)`
			const secondComment = `SELECT col_description('"s.dot"."t.dot"'::regclass,2)`
			exec := func(c *sql.Conn, q string, prepared bool) {
				t.Helper()
				step := fixture.Exec(q, 0)
				step.Prepared = prepared
				fixture.RunStep(t, ctx, c, backend.Recorder, step)
			}
			dump := func() string {
				t.Helper()
				var buffer bytes.Buffer
				if err := stuntgres.Dump(ctx, p.Native, &buffer); err != nil {
					t.Fatal(err)
				}
				return buffer.String()
			}
			diagnostic := func(t *testing.T, err error, code, message, detail string) {
				t.Helper()
				fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail})
			}
			var canonical string
			t.Run("roundtrip", func(t *testing.T) {
				exec(a, `CREATE SCHEMA "s.dot"`, false)
				exec(a, `CREATE TABLE "s.dot"."t.dot"(a INT,b TEXT)`, false)
				if _, err := a.ExecContext(ctx, `INSERT INTO "s.dot"."t.dot" VALUES(7,'sentinel')`); err != nil {
					t.Fatal(err)
				}
				for _, q := range []string{`COMMENT ON SCHEMA "s.dot" IS 'schema'`, `COMMENT ON SCHEMA public IS 'bootstrap'`, `COMMENT ON TABLE "s.dot"."t.dot" IS E'quote\' slash\\\n雪'`} {
					exec(a, q, false)
				}
				for _, q := range []string{`COMMENT ON COLUMN "s.dot"."t.dot".a IS 'first'`, `COMMENT ON COLUMN "s.dot"."t.dot".b IS 'second'`} {
					exec(a, q, true)
				}
				rows, err := a.QueryContext(ctx, "COMMENT ON SCHEMA public IS 'bootstrap'")
				if err != nil {
					t.Fatal(err)
				}
				columns, err := rows.Columns()
				if err != nil || len(columns) != 0 || rows.Next() {
					rows.Close()
					t.Fatal("COMMENT exposed result columns/rows", columns, err)
				}
				if err := rows.Err(); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
				comment(tableComment, "obj_description", "quote' slash\\\n雪")
				comment(firstComment, "col_description", "first")
				comment(secondComment, "col_description", "second")
				if backend.PG {
					comment(`SELECT obj_description('"s.dot"'::regnamespace,'pg_namespace')`, "obj_description", "schema")
					comment(`SELECT obj_description('public'::regnamespace,'pg_namespace')`, "obj_description", "bootstrap")
				}
				if !backend.PG {
					canonical = dump()
					for _, line := range []string{`COMMENT ON SCHEMA "public" IS E'bootstrap';`, `COMMENT ON SCHEMA "s.dot" IS E'schema';`, `COMMENT ON TABLE "s.dot"."t.dot" IS E'quote'' slash\\` + "\n雪';", `COMMENT ON COLUMN "s.dot"."t.dot"."a" IS E'first';`, `COMMENT ON COLUMN "s.dot"."t.dot"."b" IS E'second';`} {
						if !strings.Contains(canonical, line) {
							t.Fatal("missing independent payload", line, canonical)
						}
					}
				}
				exec(a, `COMMENT ON COLUMN "s.dot"."t.dot".a IS 'replacement'`, true)
				comment(firstComment, "col_description", "replacement")
				if !backend.PG && !strings.Contains(dump(), "E'replacement'") {
					t.Fatal("replacement absent")
				}
				exec(a, `COMMENT ON COLUMN "s.dot"."t.dot".a IS NULL`, false)
				exec(a, `COMMENT ON COLUMN "s.dot"."t.dot".b IS ''`, true)
				comment(firstComment, "col_description", nil)
				comment(secondComment, "col_description", nil)
				if !backend.PG && strings.Contains(dump(), "COMMENT ON COLUMN") {
					t.Fatal("removal serialized")
				}
				exec(a, `COMMENT ON COLUMN "s.dot"."t.dot".b IS ' '`, true)
				comment(secondComment, "col_description", " ")
				if !backend.PG && !strings.Contains(dump(), "IS E' ';") {
					t.Fatal("whitespace removed")
				}
			})
			t.Run("controls", func(t *testing.T) {
				if !backend.PG {
					if err := stuntgres.Reset(ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(canonical)); err != nil {
						t.Fatal(err)
					}
					if dump() != canonical {
						t.Fatal("noncanonical roundtrip")
					}
					var v int
					if err := a.QueryRowContext(ctx, `SELECT a FROM "s.dot"."t.dot"`).Scan(&v); err != nil || v != 7 {
						t.Fatal(v, err)
					}
					for _, suffix := range []string{`COMMENT ON COLUMN "s.dot"."t.dot".missing IS 'late';`, `COMMENT ON ROLE postgres IS 'late';`} {
						if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(canonical+`COMMENT ON TABLE "s.dot"."t.dot" IS 'earlier';`+suffix)); err == nil {
							t.Fatal("accepted bad restore")
						}
						if dump() != canonical {
							t.Fatal("bad restore changed comments")
						}
					}
					exec(a, `ALTER SCHEMA "s.dot" RENAME TO renamed`, false)
					if !backend.PG && !strings.Contains(dump(), `COMMENT ON SCHEMA "renamed" IS E'schema';`) {
						t.Fatal("rename lost comment")
					}
					if err := stuntgres.Restore(ctx, p.Native, strings.NewReader("CREATE TABLE legacy(v INT); INSERT INTO legacy VALUES(7);")); err != nil {
						t.Fatal(err)
					}
					if !backend.PG && strings.Contains(dump(), "COMMENT") {
						t.Fatal("legacy stream gained comments")
					}
				}
				if backend.PG {
					exec(a, `ALTER SCHEMA "s.dot" RENAME TO renamed`, false)
					comment(`SELECT obj_description('renamed'::regnamespace,'pg_namespace')`, "obj_description", "schema")
					comment(`SELECT obj_description('renamed."t.dot"'::regclass,'pg_class')`, "obj_description", "quote' slash\\\n雪")
				}
				t.Run("independent-owner-comments", func(t *testing.T) {
					for _, q := range []string{"CREATE USER mixed_table_owner", "CREATE USER mixed_schema_owner", "CREATE SCHEMA mixed AUTHORIZATION mixed_table_owner", "SET SESSION AUTHORIZATION mixed_table_owner", "CREATE TABLE mixed.t(v INT)", "RESET SESSION AUTHORIZATION", "ALTER SCHEMA mixed OWNER TO mixed_schema_owner"} {
						exec(a, q, false)
					}
					if _, err := a.ExecContext(ctx, "INSERT INTO mixed.t VALUES(7)"); err != nil {
						t.Fatal(err)
					}
					exec(a, "COMMENT ON TABLE mixed.t IS 'table text'", false)
					exec(a, "COMMENT ON COLUMN mixed.t.v IS 'column text'", true)
					exec(a, "COMMENT ON ROLE mixed_table_owner IS 'retained shared text'", true)
					var mixed string
					if !backend.PG {
						mixed = dump()
						for _, entry := range []string{`CREATE SCHEMA "mixed" AUTHORIZATION "mixed_schema_owner";`, `SET SESSION AUTHORIZATION 'mixed_table_owner';`, `COMMENT ON TABLE "mixed"."t" IS E'table text';`, `COMMENT ON COLUMN "mixed"."t"."v" IS E'column text';`} {
							if !strings.Contains(mixed, entry) {
								t.Fatal("mixed-owner dump lacks independent payload", entry, mixed)
							}
						}
						if err := stuntgres.Reset(ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(ctx, p.Native, strings.NewReader(mixed)); err != nil {
							t.Fatal("mixed-owner commented stream cannot replay", err)
						}
						if !backend.PG && dump() != mixed {
							t.Fatal("mixed-owner replay changed canonical stream")
						}
					}
					comment("SELECT obj_description('mixed.t'::regclass,'pg_class')", "obj_description", "table text")
					comment("SELECT col_description('mixed.t'::regclass,1)", "col_description", "column text")
					var sentinel int
					if err := a.QueryRowContext(ctx, "SELECT v FROM mixed.t").Scan(&sentinel); err != nil || sentinel != 7 {
						t.Fatal("mixed-owner replay lost sentinel", sentinel, err)
					}
					exec(a, "SET SESSION AUTHORIZATION mixed_table_owner", false)
					for _, query := range []string{"COMMENT ON TABLE mixed.t IS 'denied'", "COMMENT ON COLUMN mixed.t.v IS 'denied'"} {
						_, err := a.ExecContext(ctx, query)
						diagnostic(t, err, "42501", "permission denied for schema mixed", "")
					}
					var forbidden int
					err := a.QueryRowContext(ctx, "SELECT v FROM mixed.t").Scan(&forbidden)
					if err := (fixture.MigrationSmoke{T: t}).TransactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema mixed", Position: "15"}); err != nil {
						t.Fatal("public SELECT acquired private replay authority", err)
					}
					exec(a, "RESET SESSION AUTHORIZATION", false)
					if !backend.PG && dump() != mixed {
						t.Fatal("post-Restore denials changed mixed-owner stream")
					}
				})
			})
			t.Run("authority", func(t *testing.T) {
				for _, q := range []string{"CREATE USER owner", "CREATE USER other", "CREATE SCHEMA owned AUTHORIZATION owner", "SET SESSION AUTHORIZATION owner", "CREATE TABLE owned.t(v INT)", "COMMENT ON TABLE owned.t IS 'owner'"} {
					exec(a, q, false)
				}
				held, err := a.PrepareContext(ctx, "COMMENT ON TABLE owned.t IS 'prepared'")
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				heldFrame := fixture.HeldStatement{Executor: a, Statement: held, SQL: "COMMENT ON TABLE owned.t IS 'prepared'"}
				exec(a, "SET SESSION AUTHORIZATION other", false)
				// A separate public table keeps schema USAGE from masking ownership diagnostics.
				exec(b, "SET SESSION AUTHORIZATION owner", false)
				fixture.WithPublicSchemaOwner(t, p, backend, "owner", func() {
					exec(b, "CREATE TABLE public.auth_table(v INT)", false)
				})
				exec(b, "RESET SESSION AUTHORIZATION", false)
				for _, tc := range []struct{ q, code, message, detail string }{
					{"COMMENT ON TABLE public.auth_table IS 'bad'", "42501", "must be owner of table auth_table", ""},
					{"COMMENT ON COLUMN public.auth_table.v IS 'bad'", "42501", "must be owner of relation auth_table", ""},
					{"COMMENT ON ROLE other IS 'bad'", "42501", "permission denied", "The current user must have the CREATEROLE attribute."},
					{"COMMENT ON ROLE postgres IS 'bad'", "42501", "permission denied", "The current user must have the SUPERUSER attribute."},
					{"COMMENT ON ROLE absent IS NULL", "42704", `role "absent" does not exist`, ""},
				} {
					for _, prepared := range []bool{false, true} {
						err := m.TransactionAffected(ctx, a, tc.q, prepared, 0)
						diagnostic(t, err, tc.code, tc.message, tc.detail)
					}
				}
				if _, err := heldFrame.ExecContext(ctx, heldFrame.SQL); err == nil {
					t.Fatal("prepared authority cached")
				}
				exec(a, "RESET SESSION AUTHORIZATION", false)
				exec(a, "COMMENT ON ROLE owner IS 'shared'", false)
				exec(a, "COMMENT ON ROLE owner IS 'replacement'", true)
				if backend.PG {
					comment("SELECT shobj_description(oid,'pg_authid') FROM pg_roles WHERE rolname='owner'", "shobj_description", "replacement")
				}
				if !backend.PG && strings.Contains(dump(), "COMMENT ON ROLE") {
					t.Fatal("database dump imported role metadata")
				}
				exec(a, "DROP TABLE owned.t", false)
				exec(a, "CREATE TABLE owned.t(v INT)", false)
				exec(a, "SET SESSION AUTHORIZATION owner", false)
				_, err = heldFrame.ExecContext(ctx, heldFrame.SQL)
				diagnostic(t, err, "42501", "must be owner of table t", "")
				exec(a, "RESET SESSION AUTHORIZATION", false)
			})
			t.Run("locks-and-limits", func(t *testing.T) {
				exec(a, "CREATE TABLE locked(v INT)", false)
				exec(a, "COMMENT ON TABLE locked IS 'old'", false)
				tx, err := a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				if _, err := tx.ExecContext(ctx, "LOCK TABLE locked IN ACCESS EXCLUSIVE MODE"); err != nil {
					t.Fatal(err)
				}
				for _, q := range []string{"COMMENT ON TABLE locked IS 'new'", "COMMENT ON COLUMN locked.v IS 'column'"} {
					stmt, err := b.PrepareContext(ctx, q)
					if err != nil {
						t.Fatal("utility Prepare waited", err)
					}
					if backend.PG {
						exec(b, "SET statement_timeout TO '30ms'", false)
						_, err = stmt.ExecContext(ctx)
						if err == nil {
							t.Fatalf("%s: expected AX-blocked statement timeout", q)
						}
						diagnostic(t, err, "57014", "canceling statement due to statement timeout", "")
						exec(b, "SET statement_timeout TO '0'", false)
					} else {
						bounded, cancel := context.WithTimeout(ctx, 30*time.Millisecond)
						_, err = stmt.ExecContext(bounded)
						cancel()
						if !errors.Is(err, context.DeadlineExceeded) {
							t.Fatal("AX did not block", err)
						}
						var state interface{ SQLState() string }
						if errors.As(err, &state) {
							t.Fatal("ordinary deadline acquired SQLSTATE", err)
						}
					}
					stmt.Close()
				}
				if !backend.PG && !strings.Contains(dump(), `COMMENT ON TABLE "public"."locked" IS E'old';`) {
					t.Fatal("cancel changed comment")
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				comment("SELECT obj_description('locked'::regclass,'pg_class')", "obj_description", "old")
				exec(b, "COMMENT ON TABLE locked IS 'new'", true)
				comment("SELECT obj_description('locked'::regclass,'pg_class')", "obj_description", "new")
				tx, err = a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				_, err = tx.ExecContext(ctx, "COMMENT ON TABLE locked IS NULL")
				if !backend.PG {
					diagnostic(t, err, "0A000", "statement is unsupported in an explicit transaction", "")
				} else if err != nil {
					t.Fatal(err)
				}
				tx.Rollback()
				tx, err = a.BeginTx(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				prepared, err := tx.PrepareContext(ctx, "COMMENT ON TABLE locked IS NULL")
				if prepared != nil {
					prepared.Close()
				}
				if !backend.PG {
					diagnostic(t, err, "0A000", "statement is unsupported in an explicit transaction", "")
				} else if err != nil {
					t.Fatal(err)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				if !backend.PG && !strings.Contains(dump(), `COMMENT ON TABLE "public"."locked" IS E'new';`) {
					t.Fatal("explicit transaction rejection changed comment")
				}
				comment("SELECT obj_description('locked'::regclass,'pg_class')", "obj_description", "new")
				for _, q := range []string{"COMMENT ON TYPE integer IS 'x'", "COMMENT ON DATABASE d IS 'x'"} {
					var err error
					if backend.PG && strings.Contains(q, "TYPE") {
						tx, beginErr := a.BeginTx(ctx, nil)
						if beginErr != nil {
							t.Fatal(beginErr)
						}
						_, err = tx.ExecContext(ctx, q)
						if rollbackErr := tx.Rollback(); rollbackErr != nil {
							t.Fatal(rollbackErr)
						}
					} else if backend.PG {
						// PostgreSQL treats a missing database comment as a WARNING
						// so pg_restore can consume comments naming an old database.
						var notices []fixture.Diagnostic
						_, session, recorder := fixture.NoticeReferencePool(t, p, true, &notices)
						fixture.RunStep(t, ctx, session, recorder, fixture.Exec(q, 0))
						want := []fixture.Diagnostic{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "3D000", Message: `database "d" does not exist`}}
						if !reflect.DeepEqual(notices, want) {
							t.Fatalf("%s warnings %#v want %#v", q, notices, want)
						}
					} else {
						_, err = a.ExecContext(ctx, q)
					}
					if !backend.PG {
						var d *stuntgres.Error
						if !errors.As(err, &d) || d.Code != "0A000" {
							t.Fatal(q, err)
						}
					} else if err != nil {
						t.Fatal(q, err)
					}
				}
			})
		})
	}
}

func TestReferenceCommentAuthorization(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			owner := "comment_owner_" + fmt.Sprint(prepared)
			other := "comment_other_" + fmt.Sprint(prepared)
			fixture.RoleReferenceCleanup(t, []string{owner, other})
			fixture.SchemaReferenceCleanup(t, "comment_owned")
			p := fixture.NewScenario(t)
			for _, name := range []string{owner, other} {
				p.Step(t, fixture.Exec("CREATE USER "+name, 0))
			}
			for _, backend := range p.Backends() {
				bootstrap := "postgres"
				if backend.PG {
					bootstrap = "stuntgres"
				}
				t.Run(backend.Name, func(t *testing.T) {
					conn, err := backend.DB.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer conn.Close()
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
					run(fixture.Exec("CREATE SCHEMA comment_owned AUTHORIZATION "+owner, 0))
					run(fixture.Exec("SET SESSION AUTHORIZATION "+owner, 0))
					fixture.WithPublicSchemaOwner(t, p, backend, owner, func() {
						run(fixture.Exec("CREATE TABLE t(a INT)", 0))
					})
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
						{"COMMENT ON ROLE " + bootstrap + " IS 'bad'", "42501", "permission denied", "The current user must have the SUPERUSER attribute."},
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
