package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func smokeSchemaPublicLifecycle(t *testing.T) {
	for _, tc := range []struct {
		name      string
		setup     []string
		namespace string
		table     bool
		ordinary  bool
	}{
		{"renamed", []string{"CREATE TABLE public.t(id INT NOT NULL)", "INSERT INTO public.t VALUES(7)", "ALTER SCHEMA public RENAME TO renamed_public"}, "renamed_public", true, false},
		{"renamed_empty", []string{"ALTER SCHEMA public RENAME TO renamed_public"}, "renamed_public", false, false},
		{"missing_empty", []string{"DROP SCHEMA public"}, "", false, false},
		{"missing_nonpublic", []string{"DROP SCHEMA public", "CREATE SCHEMA other CREATE TABLE t(id INT NOT NULL)", "INSERT INTO other.t VALUES(9)"}, "other", true, false},
		{"recreated", []string{"DROP SCHEMA public", "CREATE SCHEMA public", "CREATE TABLE public.t(id INT NOT NULL)", "INSERT INTO public.t VALUES(7)"}, "public", true, false},
		{"recreated_empty", []string{"DROP SCHEMA public", "CREATE SCHEMA public AUTHORIZATION lifecycle_owner"}, "public", false, true},
		{"recreated_ordinary", []string{"DROP SCHEMA public", "CREATE SCHEMA public AUTHORIZATION lifecycle_owner", "SET SESSION AUTHORIZATION lifecycle_owner", "CREATE TABLE public.t(id INT NOT NULL)", "INSERT INTO public.t VALUES(7)", "RESET SESSION AUTHORIZATION"}, "public", true, true},
		{"renamed_transferred_collision", []string{"ALTER SCHEMA public RENAME TO renamed_public", "ALTER SCHEMA renamed_public OWNER TO lifecycle_owner", "CREATE SCHEMA public AUTHORIZATION lifecycle_other", "SET SESSION AUTHORIZATION lifecycle_other", "CREATE TABLE public.t(id INT NOT NULL)", "INSERT INTO public.t VALUES(7)", "RESET SESSION AUTHORIZATION"}, "public", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"lifecycle_owner", "lifecycle_other", "lifecycle_reader"})
			fixture.SchemaReferenceCleanup(t, "renamed_public", "other")
			p := fixture.NewScenario(t)
			t.Cleanup(func() {
				if p.Postgres == nil {
					return
				}
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if _, err := fixture.Admin.ExecContext(ctx, "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Error("repair controlled public schema before fixture reset", err)
				}
			})
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					ctx := p.Ctx
					db := backend.DB
					m := fixture.MigrationSmoke{T: t, PG: backend.PG, Native: p.Native, Recorder: backend.Recorder}
					c, err := db.Conn(ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					command := func(q string) {
						fixture.RunStep(t, ctx, c, backend.Recorder, fixture.Exec(q, 0))
					}
					command("CREATE USER lifecycle_owner")
					command("CREATE USER lifecycle_other")
					command("CREATE USER lifecycle_reader")
					for _, q := range tc.setup {
						count := int64(0)
						if strings.HasPrefix(q, "INSERT") {
							count = 1
						}
						fixture.RunStep(t, ctx, c, backend.Recorder, fixture.Exec(q, count))
					}
					if backend.PG && tc.namespace != "" {
						want := "stuntgres"
						if tc.name == "recreated_empty" || tc.name == "recreated_ordinary" {
							want = "lifecycle_owner"
						}
						if tc.name == "renamed_transferred_collision" {
							want = "lifecycle_other"
						}
						var owner string
						if err := c.QueryRowContext(ctx, "SELECT nspowner::regrole::text FROM pg_namespace WHERE nspname=$1", tc.namespace).Scan(&owner); err != nil || owner != want {
							t.Fatal("schema owner", owner, want, err)
						}
						if tc.name == "renamed_transferred_collision" {
							if err := c.QueryRowContext(ctx, "SELECT nspowner::regrole::text FROM pg_namespace WHERE nspname='renamed_public'").Scan(&owner); err != nil || owner != "lifecycle_owner" {
								t.Fatal("retained owner", owner, err)
							}
						}
					}
					var retained *sql.Rows
					if tc.table && !backend.PG {
						retained, err = c.QueryContext(ctx, "SELECT id FROM "+tc.namespace+".t")
						if err != nil {
							t.Fatal(err)
						}
						defer retained.Close()
					}
					if !backend.PG {
						var dump bytes.Buffer
						if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Reset(ctx, p.Native); err != nil {
							t.Fatal(err)
						}
						if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
							t.Fatal(dump.String(), err)
						}
						var again bytes.Buffer
						if err := stuntgres.Dump(ctx, p.Native, &again); err != nil || again.String() != dump.String() {
							t.Fatal("nonfaithful roundtrip", err, dump.String(), again.String())
						}
					}
					checkRow := func(rows *sql.Rows) {
						t.Helper()
						defer rows.Close()
						types, err := rows.ColumnTypes()
						if err != nil || len(types) != 1 || types[0].Name() != "id" || types[0].DatabaseTypeName() != "INT4" {
							t.Fatal(types, err)
						}
						if nullable, ok := types[0].Nullable(); (!backend.PG && (!ok || nullable)) || (backend.PG && ok) {
							t.Fatal("NOT NULL metadata lost")
						}
						if !rows.Next() {
							t.Fatal("missing row", rows.Err())
						}
						var id int64
						want := int64(7)
						if tc.name == "missing_nonpublic" {
							want = 9
						}
						if err := rows.Scan(&id); err != nil || id != want {
							t.Fatal(id, want, err)
						}
						if rows.Next() || rows.Err() != nil {
							t.Fatal("extra/error", rows.Err())
						}
					}
					if tc.table {
						rows, err := c.QueryContext(ctx, "SELECT id FROM "+tc.namespace+".t")
						if err != nil {
							t.Fatal(err)
						}
						checkRow(rows)
						if retained != nil {
							checkRow(retained)
						}
					}
					if tc.namespace == "" || tc.namespace == "other" || tc.namespace == "renamed_public" {
						err := m.TransactionAffected(ctx, c, "CREATE TABLE public.probe(id INT)", true, 0)
						if err := m.TransactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "3F000", Message: `schema "public" does not exist`, Position: "14"}); err != nil {
							t.Fatal(err)
						}
					}
					if tc.ordinary {
						command("SET SESSION AUTHORIZATION lifecycle_reader")
						err := m.TransactionAffected(ctx, c, "CREATE TABLE public.denied(id INT)", true, 0)
						if err := m.TransactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema public", Position: "14"}); err != nil {
							t.Fatal(err)
						}
						command("RESET SESSION AUTHORIZATION")
					}
				})
			}
		})
	}
	t.Run("restore_continuation", func(t *testing.T) {
		for _, destination := range []string{"fresh", "lower_counter"} {
			t.Run(destination, func(t *testing.T) {
				fixture.RoleReferenceCleanup(t, []string{"lifecycle_owner", "lifecycle_reader"})
				fixture.SchemaReferenceCleanup(t, "retained_public", "restored_a", "restored_b", "restored_data", "additional", "nested", "spent")
				p := fixture.NewScenario(t)
				t.Cleanup(func() {
					if p.Postgres == nil {
						return
					}
					ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
					defer cancel()
					if _, err := fixture.Admin.ExecContext(ctx, "DROP SCHEMA IF EXISTS public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Error("repair controlled public schema before fixture reset", err)
					}
				})
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						ctx := p.Ctx
						m := fixture.MigrationSmoke{T: t, PG: backend.PG, Native: p.Native, Recorder: backend.Recorder}

						open := func(name string) (*sql.DB, *sql.Conn) {
							t.Helper()
							connector, err := stuntgres.NewDriver().OpenConnector(name)
							if err != nil {
								t.Fatal(err)
							}
							db := sql.OpenDB(connector)
							t.Cleanup(func() { db.Close() })
							c, err := db.Conn(ctx)
							if err != nil {
								t.Fatal(err)
							}
							t.Cleanup(func() { c.Close() })
							return db, c
						}
						command := func(c *sql.Conn, q string, count int64) {
							t.Helper()
							fixture.RunStep(t, ctx, c, backend.Recorder, fixture.Exec(q, count))
						}
						source := backend.DB
						sourceConn, err := source.Conn(ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer sourceConn.Close()
						for _, q := range []string{
							"CREATE USER lifecycle_owner",
							"CREATE USER lifecycle_reader",
							"ALTER SCHEMA public RENAME TO retained_public",
							"ALTER SCHEMA retained_public OWNER TO lifecycle_owner",
							"CREATE SCHEMA restored_a AUTHORIZATION lifecycle_owner",
							"CREATE SCHEMA restored_b AUTHORIZATION lifecycle_owner",
							"CREATE SCHEMA restored_data AUTHORIZATION lifecycle_owner CREATE TABLE t(id INT NOT NULL)",
						} {
							command(sourceConn, q, 0)
						}
						command(sourceConn, "INSERT INTO restored_data.t VALUES(7),(9)", 2)
						db, c := source, sourceConn
						var dump bytes.Buffer
						if !backend.PG {
							if err := stuntgres.Dump(ctx, p.Native, &dump); err != nil {
								t.Fatal(err)
							}
							db, c = open(t.Name() + "/destination")
							command(c, "CREATE USER lifecycle_owner", 0)
							command(c, "CREATE USER lifecycle_reader", 0)
							if destination == "lower_counter" {
								command(c, "CREATE SCHEMA spent", 0)
								command(c, "DROP SCHEMA spent", 0)
							}
							if err := stuntgres.Restore(ctx, db, bytes.NewReader(dump.Bytes())); err != nil {
								t.Fatal(err)
							}
						}
						command(c, "CREATE SCHEMA additional", 0)
						command(c, "CREATE SCHEMA nested CREATE TABLE t(id INT NOT NULL) CREATE TABLE u(id INT NOT NULL)", 0)
						command(c, "INSERT INTO nested.t VALUES(13),(17)", 2)
						command(c, "INSERT INTO nested.u VALUES(19)", 1)
						for _, tc := range []struct {
							table string
							ids   []int64
						}{
							{"restored_data.t", []int64{7, 9}},
							{"nested.t", []int64{13, 17}},
							{"nested.u", []int64{19}},
						} {
							rows, err := c.QueryContext(ctx, "SELECT id FROM "+tc.table+" ORDER BY id")
							if err != nil {
								t.Fatal(err)
							}
							types, err := rows.ColumnTypes()
							if err != nil || len(types) != 1 || types[0].Name() != "id" || types[0].DatabaseTypeName() != "INT4" {
								rows.Close()
								t.Fatal(types, err)
							}
							if nullable, ok := types[0].Nullable(); (!backend.PG && (!ok || nullable)) || (backend.PG && ok) {
								rows.Close()
								t.Fatal("NOT NULL metadata lost")
							}
							for _, want := range tc.ids {
								if !rows.Next() {
									rows.Close()
									t.Fatal("missing row", tc.table, rows.Err())
								}
								var got int64
								if err := rows.Scan(&got); err != nil || got != want {
									rows.Close()
									t.Fatal(tc.table, got, want, err)
								}
							}
							if rows.Next() || rows.Err() != nil {
								rows.Close()
								t.Fatal("extra row/error", tc.table, rows.Err())
							}
							rows.Close()
						}
						command(c, "SET SESSION AUTHORIZATION lifecycle_reader", 0)
						err = m.TransactionAffected(ctx, c, "CREATE TABLE restored_data.denied(id INT)", true, 0)
						if err := m.TransactionDiagnostic(err, stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema restored_data", Position: "14"}); err != nil {
							t.Fatal(err)
						}
						command(c, "RESET SESSION AUTHORIZATION", 0)
						command(c, "DROP SCHEMA additional,nested CASCADE", 0)
						if !backend.PG {
							var again bytes.Buffer
							if err := stuntgres.Dump(ctx, db, &again); err != nil || again.String() != dump.String() {
								t.Fatal("restored owners/ACLs/rows changed", err, again.String(), dump.String())
							}
						}
						// The retained builtin still has its canonical PUBLIC CREATE grant.
						if backend.PG {
							command(c, "GRANT CREATE ON SCHEMA retained_public TO PUBLIC", 0)
						}
						command(c, "SET SESSION AUTHORIZATION lifecycle_reader", 0)
						command(c, "CREATE TABLE retained_public.probe(id INT)", 0)
						command(c, "RESET SESSION AUTHORIZATION", 0)
						command(c, "SET SESSION AUTHORIZATION lifecycle_owner", 0)
						command(c, "CREATE TABLE restored_a.owner_probe(id INT)", 0)
						command(c, "INSERT INTO restored_a.owner_probe VALUES(23)", 1)
						var got int64
						if err := c.QueryRowContext(ctx, "SELECT id FROM restored_a.owner_probe").Scan(&got); err != nil || got != 23 {
							t.Fatal("owner probe row", got, err)
						}
					})
				}
			})
		}
	})
}
