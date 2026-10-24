package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTriggerRoutes(t *testing.T) {
	t.Run("retained-messages", transactionalTriggerDriverMessages)
	t.Run("command-routes", transactionalTriggerCommandRoutes)
	for _, api := range []string{"auto", "sql", "go"} {
		for _, held := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				if api == "auto" && !commit {
					continue
				}
				t.Run(fmt.Sprintf("%s/held=%v/commit=%v", api, held, commit), func(t *testing.T) {
					triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						triggerSetup(t, p, b, c)
						userOperatorExec(t, p, b, c, "CREATE EXTENSION unaccent", "CREATE TABLE tr_unrelated(id integer)", "CREATE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()")
						peer, err := b.DB.Conn(p.Ctx)
						if err != nil {
							t.Fatal(err)
						}
						defer peer.Close()
						queries := []string{
							"CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)",
							"CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(7)",
							`ALTER TRIGGER z ON tr_data RENAME TO "0double"`,
							"ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent",
							"ALTER TRIGGER a ON tr_data NO DEPENDS ON EXTENSION unaccent",
							"DROP TRIGGER a ON tr_data",
						}
						insert := "INSERT INTO tr_data VALUES($1,5) RETURNING id,v"
						statements := make(map[string]*sql.Stmt)
						if held {
							for _, q := range append(append([]string{}, queries...), insert) {
								s, err := c.PrepareContext(p.Ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer s.Close()
								statements[q] = s
							}
						}
						var e fixture.Executor = c
						var tx *sql.Tx
						if api == "go" {
							tx, err = c.BeginTx(p.Ctx, nil)
							if err != nil {
								t.Fatal(err)
							}
							defer tx.Rollback()
							e = tx
							for q, s := range statements {
								bound := tx.StmtContext(p.Ctx, s)
								defer bound.Close()
								statements[q] = bound
							}
						} else if api == "sql" {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("BEGIN", 0))
							defer c.ExecContext(context.Background(), "ROLLBACK")
						}
						run := func(s fixture.Step) {
							var target fixture.Executor = e
							if stmt := statements[s.Query]; stmt != nil {
								target = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: s.Query}
							}
							fixture.RunStep(t, p.Ctx, target, b.Recorder, s)
						}
						if api != "auto" {
							run(fixture.Exec("SAVEPOINT baseline", 0))
						}
						values := []int64{16, 24, 17, 17, 17, 10}
						for i, q := range queries {
							run(fixture.Exec(q, 0))
							s := triggerRows(insert, []any{int64(i + 1), values[i]})
							s.Args = []any{i + 1}
							run(s)
							if i == 3 {
								run(triggerDDLX("tr_data", "unaccent", 1))
							}
							if i == 4 {
								run(triggerDDLX("tr_data", "unaccent", 0))
							}
							if i == 0 && api != "auto" {
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerRows("SELECT id,v FROM tr_data"))
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("INSERT INTO tr_unrelated VALUES(71)", 1))
							}
						}
						if api != "auto" {
							run(fixture.Exec("ROLLBACK TO baseline", 0))
							run(triggerRows("SELECT id,v FROM tr_data"))
							// Execute the original CREATE recipe again after rolling back its authority.
							run(fixture.Exec(queries[0], 0))
							s := triggerRows(insert, []any{int64(80), int64(16)})
							s.Args = []any{80}
							run(s)
							run(fixture.Exec("RELEASE baseline", 0))
							if tx != nil {
								if commit {
									err = tx.Commit()
								} else {
									err = tx.Rollback()
								}
								if err != nil {
									t.Fatal(err)
								}
							} else {
								q := "ROLLBACK"
								if commit {
									q = "COMMIT"
								}
								run(fixture.Exec(q, 0))
							}
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT id FROM tr_unrelated", []string{"id"}, []string{"INT4"}, [][]any{{int64(71)}}))
							rows := triggerRows("SELECT id,v FROM tr_data")
							if commit {
								rows = triggerRows(rows.Query, []any{int64(80), int64(16)})
							}
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, rows)
						}
						want := int64(10)
						if api != "auto" && commit {
							want = 16
						}
						fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerRows("INSERT INTO tr_data VALUES(99,5) RETURNING id,v", []any{int64(99), want}))
					})
				})
			}
		}
	}
}

func transactionalTriggerDriverMessages(t *testing.T) {
	triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		triggerSetup(t, p, b, c)
		userOperatorExec(t, p, b, c, "CREATE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()")
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		defer c.ExecContext(context.Background(), "ROLLBACK")
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		create := "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)"
		q := "INSERT INTO tr_data VALUES($1,5) RETURNING id,v"
		held := triggerDDLHeld(t, p, b, c, q)
		insert := func(id, value int64) { s := triggerRows(q, []any{id, value}); s.Args = []any{id}; held(s) }
		run(triggerDDLError(create+"; INSERT INTO tr_data VALUES(1,5); SELECT 1/0; INSERT INTO tr_data VALUES(2,5)", "22012", "division by zero", false))
		run(triggerRows("SELECT id,v FROM tr_data"))
		insert(3, 10)
		run(fixture.Exec(create+"; INSERT INTO tr_data VALUES(4,5); BEGIN", 0))
		run(triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(3), int64(10)}, []any{int64(4), int64(16)}))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerRows("SELECT id,v FROM tr_data", []any{int64(3), int64(10)}))
		run(fixture.Exec("ROLLBACK", 0))
		insert(5, 10)
		run(triggerDDLError(create+"; INSERT INTO tr_data VALUES(6,5); BEGIN; COMMIT; SELECT 1/0", "22012", "division by zero", false))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerRows("SELECT id,v FROM tr_data ORDER BY id", []any{int64(3), int64(10)}, []any{int64(5), int64(10)}, []any{int64(6), int64(16)}))
		insert(7, 16)
		run(fixture.Exec("DROP TRIGGER a ON tr_data", 0))
		bad := create + "; INSERT INTO tr_data VALUES(8,5); COMMIT; SELECT FROM"
		d := fixture.ControlsReferenceDiagnostic("42601", "syntax error at end of input")
		d.Position = fixture.Position(len(bad) + 1)
		run(fixture.ErrorDiagnostic(bad, d))
		run(triggerRows("SELECT id,v FROM tr_data WHERE id=8"))
		insert(9, 10)
		run(fixture.Exec(create+"; INSERT INTO tr_data VALUES(10,5)", 1))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerRows("SELECT id,v FROM tr_data WHERE id=10", []any{int64(10), int64(16)}))
		insert(11, 16)
	})
}

func transactionalTriggerCommandRoutes(t *testing.T) {
	cases := []struct {
		name, q                        string
		before, after, xBefore, xAfter int64
	}{
		{"create", "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)", 10, 16, 0, 0},
		{"replace", "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(7)", 16, 24, 0, 0},
		{"rename", `ALTER TRIGGER z ON tr_data RENAME TO "0double"`, 16, 13, 0, 0},
		{"drop", "DROP TRIGGER a ON tr_data", 16, 10, 0, 0},
		{"depends", "ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent", 16, 16, 0, 1},
		{"no-depends", "ALTER TRIGGER a ON tr_data NO DEPENDS ON EXTENSION unaccent", 16, 16, 1, 0},
	}
	for _, tc := range cases {
		for _, api := range []string{"auto", "sql", "go"} {
			for _, held := range []bool{false, true} {
				for _, commit := range []bool{false, true} {
					if api == "auto" && !commit {
						continue
					}
					t.Run(fmt.Sprintf("%s/%s/held=%v/commit=%v", tc.name, api, held, commit), func(t *testing.T) {
						triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
							triggerSetup(t, p, b, c)
							userOperatorExec(t, p, b, c, "CREATE EXTENSION unaccent", "CREATE TRIGGER z BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()")
							if tc.name != "create" {
								userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)")
							}
							if tc.xBefore == 1 {
								userOperatorExec(t, p, b, c, "ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent")
							}
							peer, err := b.DB.Conn(p.Ctx)
							if err != nil {
								t.Fatal(err)
							}
							defer peer.Close()
							var stmt *sql.Stmt
							if held {
								stmt, err = c.PrepareContext(p.Ctx, tc.q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
							}
							var e fixture.Executor = c
							var tx *sql.Tx
							if api == "go" {
								tx, err = c.BeginTx(p.Ctx, nil)
								if err != nil {
									t.Fatal(err)
								}
								defer tx.Rollback()
								e = tx
								if held {
									stmt = tx.StmtContext(p.Ctx, stmt)
									defer stmt.Close()
								}
							} else if api == "sql" {
								fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
								defer c.ExecContext(context.Background(), "ROLLBACK")
							}
							run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
							var utility fixture.Executor = e
							if held {
								utility = fixture.HeldStatement{Executor: e, Statement: stmt, SQL: tc.q}
							}
							execute := func() {
								fixture.RunStep(t, p.Ctx, utility, b.Recorder, fixture.Exec(tc.q, 0))
								run(triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), tc.after}))
								run(triggerDDLX("tr_data", "unaccent", tc.xAfter))
							}
							if api != "auto" {
								run(fixture.Exec("SAVEPOINT command", 0))
							}
							execute()
							if api != "auto" {
								fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerDDLX("tr_data", "unaccent", tc.xBefore))
								run(fixture.Exec("ROLLBACK TO command", 0))
								run(triggerRows("SELECT id,v FROM tr_data"))
								run(triggerDDLX("tr_data", "unaccent", tc.xBefore))
								execute()
								run(fixture.Exec("RELEASE command", 0))
								if tx != nil {
									if commit {
										err = tx.Commit()
									} else {
										err = tx.Rollback()
									}
									if err != nil {
										t.Fatal(err)
									}
								} else {
									q := "ROLLBACK"
									if commit {
										q = "COMMIT"
									}
									run(fixture.Exec(q, 0))
								}
							}
							rows := triggerRows("SELECT id,v FROM tr_data")
							want, x := tc.before, tc.xBefore
							if commit {
								rows = triggerRows(rows.Query, []any{int64(1), tc.after})
								want, x = tc.after, tc.xAfter
							}
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, rows)
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerDDLX("tr_data", "unaccent", x))
							fixture.RunStep(t, p.Ctx, peer, b.Recorder, triggerRows("INSERT INTO tr_data VALUES(2,5) RETURNING id,v", []any{int64(2), want}))
						})
					})
				}
			}
		}
	}
}
