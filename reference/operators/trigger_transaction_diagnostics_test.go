package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTriggerDiagnostics(t *testing.T) {
	cases := []struct {
		name, query, code, message, repair string
	}{
		{"wrong-table-rename", "ALTER TRIGGER a ON tr_other RENAME TO renamed", "42704", `trigger "a" for table "tr_other" does not exist`, "CREATE TRIGGER a BEFORE INSERT ON tr_other FOR EACH ROW EXECUTE FUNCTION tr_double()"},
		{"wrong-table-drop", "DROP TRIGGER a ON public.tr_other", "42704", `trigger "a" for table "tr_other" does not exist`, "CREATE TRIGGER a BEFORE INSERT ON tr_other FOR EACH ROW EXECUTE FUNCTION tr_double()"},
		{"wrong-table-depends", "ALTER TRIGGER a ON tr_other DEPENDS ON EXTENSION unaccent", "42704", `trigger "a" for table "tr_other" does not exist`, "CREATE TRIGGER a BEFORE INSERT ON tr_other FOR EACH ROW EXECUTE FUNCTION tr_double()"},
		{"wrong-table-no-depends", "ALTER TRIGGER a ON tr_other NO DEPENDS ON EXTENSION unaccent", "42704", `trigger "a" for table "tr_other" does not exist`, "CREATE TRIGGER a BEFORE INSERT ON tr_other FOR EACH ROW EXECUTE FUNCTION tr_double()"},
		{"missing-extension-depends", "ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION fuzzystrmatch", "42704", `extension "fuzzystrmatch" does not exist`, ""},
		{"missing-extension-no-depends", "ALTER TRIGGER a ON tr_data NO DEPENDS ON EXTENSION fuzzystrmatch", "42704", `extension "fuzzystrmatch" does not exist`, ""},
		{"rename-collision", "ALTER TRIGGER a ON tr_data RENAME TO z", "42710", `trigger "z" for relation "tr_data" already exists`, "DROP TRIGGER z ON tr_data"},
		{"notice-wrong-table", "DROP TRIGGER IF EXISTS a ON tr_other", "00000", `trigger "a" for relation "tr_other" does not exist, skipping`, ""},
		{"notice-qualified-table", "DROP TRIGGER IF EXISTS a ON public.tr_other", "00000", `trigger "a" for relation "public.tr_other" does not exist, skipping`, ""},
		{"notice-quoted-name", `DROP TRIGGER IF EXISTS "a 名" ON public.tr_data`, "00000", `trigger "a 名" for relation "public.tr_data" does not exist, skipping`, ""},
		{"notice-view", "DROP TRIGGER IF EXISTS a ON tr_view", "00000", `trigger "a" for relation "tr_view" does not exist, skipping`, ""},
		{"notice-table", "DROP TRIGGER IF EXISTS a ON public.no_table", "00000", `relation "public.no_table" does not exist, skipping`, ""},
		{"notice-schema", "DROP TRIGGER IF EXISTS a ON no_schema.no_table", "00000", `schema "no_schema" does not exist, skipping`, ""},
	}
	for _, tc := range cases {
		for _, api := range []string{"auto", "sql", "go"} {
			for _, held := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/held=%v", tc.name, api, held), func(t *testing.T) {
					triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
						records := new([]fixture.Diagnostic)
						_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, records)
						defer c.ExecContext(context.Background(), "ROLLBACK")
						triggerDDLChain(t, p, b, c)
						userOperatorExec(t, p, b, c,
							"CREATE TABLE tr_other(id integer,v integer)",
							"CREATE VIEW tr_view AS SELECT id,v FROM tr_data",
							"CREATE EXTENSION unaccent",
							"ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent")
						fixture.RunStep(t, p.Ctx, c, recorder, fixture.Exec("INSERT INTO tr_data VALUES(0,5)", 1))
						*records = nil
						var original *sql.Stmt
						var err error
						if held {
							original, err = c.PrepareContext(p.Ctx, tc.query)
							if err != nil {
								t.Fatal("quiet prepare", err)
							}
							defer original.Close()
						}
						if len(*records) != 0 {
							t.Fatalf("preparation emitted notices: %#v", *records)
						}
						var e fixture.Executor = c
						var tx *sql.Tx
						stmt := original
						if api == "go" {
							tx, err = c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
							if held {
								stmt = tx.StmtContext(p.Ctx, original)
								defer stmt.Close()
							}
						} else if api == "sql" {
							fixture.RunStep(t, p.Ctx, c, recorder, fixture.Exec("BEGIN", 0))
						}
						run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, recorder, s) }
						var utility fixture.Executor = e
						if held {
							utility = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: tc.query}
						}
						execute := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, utility, recorder, s) }
						// Keep a committed predecessor row and verify real firing, not just
						// catalog absence. Every scratch row is removed before the next probe.
						predecessor := func() {
							run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(0), int64(16)}))
							run(triggerDDLX("tr_data", "unaccent", 1))
							run(triggerDDLX("tr_other", "unaccent", 0))
							run(triggerRows("INSERT INTO tr_data VALUES(10,5) RETURNING id,v", []any{int64(10), int64(16)}))
							run(fixture.Exec("DELETE FROM tr_data WHERE id=10", 1))
							run(triggerRows("INSERT INTO tr_other VALUES(11,5) RETURNING id,v", []any{int64(11), int64(5)}))
							run(fixture.Exec("DELETE FROM tr_other WHERE id=11", 1))
						}
						if api != "auto" {
							run(fixture.Exec("SAVEPOINT diagnostic", 0))
						}
						if tc.code == "00000" {
							for range 2 {
								*records = nil
								execute(fixture.Exec(tc.query, 0))
								want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: tc.message}
								if len(*records) != 1 || (*records)[0] != want {
									t.Fatalf("notices %#v want %#v", *records, want)
								}
								// Conditional absence is success and must not abort the owner.
								run(fixture.Select("SELECT 1 AS healthy", []string{"healthy"}, []string{"INT4"}, [][]any{{int64(1)}}))
								predecessor()
								if api != "auto" {
									run(fixture.Exec("ROLLBACK TO diagnostic", 0))
								}
							}
						} else {
							execute(triggerDDLError(tc.query, tc.code, tc.message, false))
							if len(*records) != 0 {
								t.Fatalf("failure emitted notices: %#v", *records)
							}
							if api != "auto" {
								execute(triggerDDLError(tc.query, "25P02", "current transaction is aborted, commands ignored until end of transaction block", false))
								run(fixture.Exec("ROLLBACK TO diagnostic", 0))
							}
							predecessor()
							if tc.repair == "" {
								// Install the real missing provider independently, never inside
								// the owner transaction or through a widened utility policy.
								admin, err := b.DB.Conn(p.Ctx)
								if err != nil {
									t.Fatal(err)
								}
								fixture.RunStep(t, p.Ctx, admin, b.Recorder, fixture.Exec("CREATE EXTENSION fuzzystrmatch", 0))
								if err := admin.Close(); err != nil {
									t.Fatal(err)
								}
							}
							for range 2 {
								if tc.repair != "" {
									run(fixture.Exec(tc.repair, 0))
								}
								*records = nil
								// Reuse the original utility recipe after recovery and repair.
								execute(fixture.Exec(tc.query, 0))
								if len(*records) != 0 {
									t.Fatalf("repaired execution emitted notices: %#v", *records)
								}
								mainValue, otherValue, otherX := int64(16), int64(5), int64(0)
								switch tc.name {
								case "rename-collision":
									mainValue = 8
								case "wrong-table-rename", "wrong-table-no-depends":
									otherValue = 10
								case "wrong-table-depends":
									otherValue, otherX = 10, 1
								}
								run(triggerRows("INSERT INTO tr_data VALUES(10,5) RETURNING id,v", []any{int64(10), mainValue}))
								run(fixture.Exec("DELETE FROM tr_data WHERE id=10", 1))
								run(triggerRows("INSERT INTO tr_other VALUES(11,5) RETURNING id,v", []any{int64(11), otherValue}))
								run(fixture.Exec("DELETE FROM tr_other WHERE id=11", 1))
								run(triggerDDLX("tr_data", "unaccent", 1))
								run(triggerDDLX("tr_other", "unaccent", otherX))
								if tc.repair == "" {
									x := int64(0)
									if tc.name == "missing-extension-depends" {
										x = 1
									}
									run(triggerDDLX("tr_data", "fuzzystrmatch", x))
								}
								if api != "auto" {
									run(fixture.Exec("ROLLBACK TO diagnostic", 0))
									predecessor()
									run(triggerDDLX("tr_data", "fuzzystrmatch", 0))
								} else {
									break
								}
							}
						}
						if api != "auto" {
							run(fixture.Exec("RELEASE diagnostic", 0))
							if tx != nil {
								if err := tx.Commit(); err != nil {
									t.Fatal(err)
								}
							} else {
								run(fixture.Exec("COMMIT", 0))
							}
							fixture.RunStep(t, p.Ctx, c, recorder, triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(0), int64(16)}))
							fixture.RunStep(t, p.Ctx, c, recorder, triggerRows("INSERT INTO tr_data VALUES(20,5) RETURNING id,v", []any{int64(20), int64(16)}))
						}
					})
				})
			}
		}
	}
}
