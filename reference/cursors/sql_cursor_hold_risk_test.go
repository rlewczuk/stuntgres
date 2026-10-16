package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLCursorHoldRestrictions(t *testing.T) {
	for _, strength := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
		t.Run(strength, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				q := "DECLARE h NO SCROLL CURSOR WITH HOLD FOR SELECT i FROM cursor_rows FOR " + strength
				want := sqlCursorDiagnostic("0A000", "DECLARE CURSOR WITH HOLD ... FOR "+strength+" is not supported")
				want.Detail = "Holdable cursors must be READ ONLY."
				stmt, err := c.PrepareContext(p.Ctx, q)
				if stmt != nil {
					stmt.Close()
					t.Fatal("restricted Prepare returned handle")
				}
				fixture.AssertDiagnostic(t, err, &want)
				sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic(q, want))
				// Contained-query analysis precedes the hold restriction. The
				// positions refer to these exact SQL strings, on both API routes.
				for _, invalid := range []struct {
					query string
					want  fixture.Diagnostic
				}{
					{"DECLARE h CURSOR WITH HOLD FOR SELECT absent FROM cursor_rows FOR " + strength,
						func() fixture.Diagnostic {
							d := sqlCursorDiagnostic("42703", "column \"absent\" does not exist")
							d.Position = "39"
							return d
						}()},
					{"DECLARE h CURSOR WITH HOLD FOR SELECT i FROM hold_absent FOR " + strength,
						func() fixture.Diagnostic {
							d := sqlCursorDiagnostic("42P01", "relation \"hold_absent\" does not exist")
							d.Position = "46"
							return d
						}()},
				} {
					sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic(invalid.query, invalid.want))
					stmt, err := c.PrepareContext(p.Ctx, invalid.query)
					if stmt != nil {
						stmt.Close()
						t.Fatal("invalid contained query prepared")
					}
					fixture.AssertDiagnostic(t, err, &invalid.want)
				}
			})
		})
	}
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sqlCursorSetup(t, p, b, c)
		holdRun(t, p, b, c, holdDeclare("h"))
		holdRun(t, p, b, c, "BEGIN")
		sqlCursorError(t, p, b, c, "DELETE FROM cursor_rows WHERE CURRENT OF h", sqlCursorDiagnostic("24000", "cursor \"h\" is held from a previous transaction"))
		holdRun(t, p, b, c, "ROLLBACK")
	})
}

func TestReferenceSQLCursorHoldNestedRowLocks(t *testing.T) {
	for _, route := range []string{"derived", "view"} {
		t.Run(route, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				source := "(SELECT i,t FROM cursor_rows FOR UPDATE) AS d"
				if route == "view" {
					holdRun(t, p, b, c, "CREATE VIEW hold_lock_view AS SELECT i,t FROM cursor_rows FOR UPDATE")
					source = "hold_lock_view"
				}
				q := "DECLARE h NO SCROLL CURSOR WITH HOLD FOR SELECT i,t FROM " + source + " ORDER BY i"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("admitted nested rowmark Prepare", err)
				}
				stmt.Close()
				holdRun(t, p, b, c, "BEGIN")
				holdRun(t, p, b, c, q)
				holdRun(t, p, b, c, "COMMIT")
				peer, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer peer.Close()
				holdRun(t, p, b, peer, "SET lock_timeout='500ms'")
				sqlCursorStep(t, p, b, peer, fixture.Exec("UPDATE cursor_rows SET t='peer' WHERE i=2", 1))
				// The stored view retains its declared nullable metadata; the
				// derived base-source column retains NOT NULL. No source reopen.
				sqlCursorStep(t, p, b, c, sqlCursorBatchMetadata("FETCH ALL FROM h", [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}, b.PG, []bool{route == "view", true}))
			})
		})
	}
}

// The OFFSET0 source boundary is the existing DeclarationTiming convention:
// sorting the source cannot demand the division target. First FETCH returns -10;
// COMMIT's remaining demand reaches i=2 and fails, without planning-time folding.
const holdLateDivision = "SELECT i,10/(i-2) AS v FROM (SELECT i FROM cursor_rows ORDER BY i OFFSET 0) AS ordered"

func TestReferenceSQLCursorHoldFailureAndPromotion(t *testing.T) {
	for _, route := range []string{"sql", "go", "implicit", "before-first"} {
		t.Run(route, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				holdRun(t, p, b, c, "CREATE TABLE hold_private(i INTEGER)")
				holdRun(t, p, b, c, holdDeclare("old"))
				var x fixture.Executor = c
				var tx *sql.Tx
				if route == "go" {
					var err error
					tx, err = c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					x = tx
				} else if route != "implicit" {
					holdRun(t, p, b, c, "BEGIN")
				}
				q := "DECLARE broken NO SCROLL CURSOR WITH HOLD FOR " + holdLateDivision
				want := sqlCursorDiagnostic("22012", "division by zero")
				if route == "implicit" {
					sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic(q, want))
				} else {
					sqlCursorStep(t, p, b, x, fixture.Exec("INSERT INTO hold_private VALUES(9)", 1))
					holdRun(t, p, b, x, q)
					if route != "before-first" {
						sqlCursorStep(t, p, b, x, fixture.Select("FETCH 1 FROM broken", []string{"i", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(-10)}}))
					}
					if tx != nil {
						fixture.AssertDiagnostic(t, tx.Commit(), &want)
					} else {
						sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("COMMIT", want))
					}
				}
				c.ExecContext(p.Ctx, "ROLLBACK")
				sqlCursorStep(t, p, b, c, fixture.Select("SELECT i FROM hold_private", []string{"i"}, []string{"INT4"}, [][]any{}))
				sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM broken", sqlCursorMissing("broken")))
				holdBatch(t, p, b, c, "FETCH 1 FROM old", []any{int64(1), "one"})
			})
		})
	}
	holdPartialPromotion(t)
	t.Run("ssi_publication", holdSSIPublication)
}

// Promotion is per portal, not transaction-atomic. The traversal order is not
// PostgreSQL's public contract: sequence state independently identifies which
// portals drained, then determines exact lifetime/rows without an order golden.
func holdPartialPromotion(t *testing.T) {
	promotions := map[string]int{}
	seen := map[string]bool{}
	for _, failureName := range []string{"a_portal", "m_portal", "z_portal"} {
		t.Run("portals_"+failureName, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				seen[b.Name] = true
				sqlCursorSetup(t, p, b, c)
				holdRun(t, p, b, c, "CREATE TABLE hold_private(i INTEGER)")
				holdRun(t, p, b, c, holdDeclare("old"))
				names := []string{"a_portal", "m_portal", "z_portal"}
				for _, name := range names {
					holdRun(t, p, b, c, "CREATE SEQUENCE "+name+"_draw START 10 CACHE 1")
				}
				holdRun(t, p, b, c, "BEGIN")
				sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO hold_private VALUES(9)", 1))
				for _, name := range names {
					query := "SELECT i,nextval('" + name + "_draw') AS n FROM cursor_rows ORDER BY i"
					if name == failureName {
						query = holdLateDivision
					}
					holdRun(t, p, b, c, "DECLARE "+name+" NO SCROLL CURSOR WITH HOLD FOR "+query)
				}
				sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("COMMIT", sqlCursorDiagnostic("22012", "division by zero")))
				c.ExecContext(p.Ctx, "ROLLBACK")
				sqlCursorStep(t, p, b, c, fixture.Select("SELECT i FROM hold_private", []string{"i"}, []string{"INT4"}, [][]any{}))
				holdBatch(t, p, b, c, "FETCH 1 FROM old", []any{int64(1), "one"})
				for _, name := range names {
					if name == failureName {
						continue
					}
					called := holdPromotionDraw(t, p, b, c, name+"_draw")
					if called {
						promotions[b.Name]++
						sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM "+name, []string{"i", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(11)}, {int64(3), int64(12)}}))
						sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM "+name, []string{"i", "n"}, []string{"INT4", "INT8"}, [][]any{}))
					} else {
						sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM "+name, sqlCursorMissing(name)))
					}
					if again := holdPromotionDraw(t, p, b, c, name+"_draw"); again != called {
						t.Fatal("postfailure FETCH replayed target", name)
					}
				}
				sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM "+failureName, sqlCursorMissing(failureName)))
			})
		})
	}
	for backend := range seen {
		if promotions[backend] == 0 {
			t.Errorf("%s: rotations never witnessed a fully drained portal before failure", backend)
		}
	}
}

func holdPromotionDraw(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, sequence string) bool {
	t.Helper()
	var called bool
	s := fixture.Select("SELECT last_value,is_called FROM "+sequence, []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{nil, nil}})
	s.CheckScan = func(t *testing.T, rows *sql.Rows, row int) {
		var last int64
		if err := rows.Scan(&last, &called); err != nil {
			t.Fatal(err)
		}
		// A healthy three-row candidate either never opened or fully drained.
		// No partially promoted healthy store is acceptable on this fixture.
		if (!called && last != 10) || (called && last != 12) {
			t.Fatalf("candidate draw state %s: %d/%t", sequence, last, called)
		}
		s.Rows[row] = []any{last, called}
	}
	sqlCursorStep(t, p, b, c, s)
	return called
}

func holdSSIPublication(t *testing.T) {
	sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		defer peer.ExecContext(p.Ctx, "ROLLBACK")
		holdRun(t, p, b, c, "CREATE TABLE hold_ssi(class INTEGER,v INTEGER)")
		sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO hold_ssi VALUES(1,10),(2,20)", 2))
		holdRun(t, p, b, c, "CREATE SEQUENCE hold_ssi_draw START 10 CACHE 1")
		holdRun(t, p, b, c, "BEGIN ISOLATION LEVEL SERIALIZABLE")
		holdRun(t, p, b, peer, "BEGIN ISOLATION LEVEL SERIALIZABLE")
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT v FROM hold_ssi WHERE class=1", []string{"v"}, []string{"INT4"}, [][]any{{int64(10)}}))
		sqlCursorStep(t, p, b, peer, fixture.Select("SELECT v FROM hold_ssi WHERE class=2", []string{"v"}, []string{"INT4"}, [][]any{{int64(20)}}))
		sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO hold_ssi VALUES(2,30)", 1))
		sqlCursorStep(t, p, b, peer, fixture.Exec("INSERT INTO hold_ssi VALUES(1,40)", 1))
		holdRun(t, p, b, c, "DECLARE ssi_hold NO SCROLL CURSOR WITH HOLD FOR SELECT nextval('hold_ssi_draw') AS n")
		holdRun(t, p, b, peer, "COMMIT")
		want := sqlCursorDiagnostic("40001", "could not serialize access due to read/write dependencies among transactions")
		want.Detail = "Reason code: Canceled on identification as a pivot, during commit attempt."
		want.Hint = "The transaction might succeed if retried."
		sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("COMMIT", want))
		c.ExecContext(p.Ctx, "ROLLBACK")
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT last_value,is_called FROM hold_ssi_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(10), true}}))
		sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM ssi_hold", []string{"n"}, []string{"INT8"}, [][]any{{int64(10)}}))
		sqlCursorStep(t, p, b, c, fixture.Select("FETCH ALL FROM ssi_hold", []string{"n"}, []string{"INT8"}, [][]any{}))
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT last_value FROM hold_ssi_draw", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(10)}}))
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT class,v FROM hold_ssi ORDER BY class,v", []string{"class", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(1), int64(40)}, {int64(2), int64(20)}}))
	})
}

func TestReferenceSQLCursorHoldPrepareBoundaries(t *testing.T) {
	for _, mode := range []string{"live", "close", "close-all", "rollback-to", "released-child", "old", "temp", "empty-gid", "failed-portal"} {
		t.Run(mode, func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqlCursorSetup(t, p, b, c)
				holdRun(t, p, b, c, "CREATE TABLE hold_prepare_private(i INTEGER)")
				holdRun(t, p, b, c, holdDeclare("old"))
				holdRun(t, p, b, c, "BEGIN")
				sqlCursorStep(t, p, b, c, fixture.Exec("INSERT INTO hold_prepare_private VALUES(9)", 1))
				if mode == "rollback-to" || mode == "released-child" {
					holdRun(t, p, b, c, "SAVEPOINT s")
				}
				if mode != "old" {
					q := holdDeclare("h")
					if mode == "failed-portal" {
						q = "DECLARE h NO SCROLL CURSOR WITH HOLD FOR " + holdLateDivision
					}
					holdRun(t, p, b, c, q)
				}
				if mode == "failed-portal" {
					holdRun(t, p, b, c, "SAVEPOINT s")
				}
				switch mode {
				case "close":
					holdRun(t, p, b, c, "CLOSE h")
				case "close-all":
					holdRun(t, p, b, c, "CLOSE ALL")
				case "rollback-to":
					holdRun(t, p, b, c, "ROLLBACK TO s")
				case "released-child":
					holdRun(t, p, b, c, "RELEASE s")
				case "temp":
					holdRun(t, p, b, c, "CREATE TEMP TABLE hold_temp(i INTEGER)")
				case "failed-portal":
					sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic("FETCH ALL FROM h", sqlCursorDiagnostic("22012", "division by zero")))
					holdRun(t, p, b, c, "ROLLBACK TO s")
				}
				gid := "hold_boundary"
				if mode == "empty-gid" {
					gid = ""
				}
				defer c.ExecContext(p.Ctx, "ROLLBACK PREPARED '"+gid+"'")
				query := "PREPARE TRANSACTION '" + gid + "'"
				if mode == "live" || mode == "released-child" || mode == "temp" || mode == "empty-gid" {
					sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic(query, sqlCursorDiagnostic("0A000", "cannot PREPARE a transaction that has created a cursor WITH HOLD")))
					c.ExecContext(p.Ctx, "ROLLBACK")
				} else {
					holdRun(t, p, b, c, query)
					holdRun(t, p, b, c, "ROLLBACK PREPARED '"+gid+"'")
				}
				sqlCursorStep(t, p, b, c, fixture.Select("SELECT i FROM hold_prepare_private", []string{"i"}, []string{"INT4"}, [][]any{}))
				if mode != "close-all" {
					holdBatch(t, p, b, c, "FETCH 1 FROM old", []any{int64(1), "one"})
				}
			})
		})
	}
}
