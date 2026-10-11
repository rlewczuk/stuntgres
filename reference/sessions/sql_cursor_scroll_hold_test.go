package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLCursorScrollHoldCommitPosition(t *testing.T) {
	// Independent release-source expectations: SCROLL exports the full result
	// and restores the saved on-row/endpoint position.
	for _, initial := range []string{"before", "on", "last", "after", "empty"} {
		t.Run(initial, func(t *testing.T) {
			sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
				sqlCursorSetup(t, p, b, c)
				step(fixture.Exec("BEGIN", 0))
				query := "SELECT i,t FROM cursor_rows ORDER BY i"
				if initial == "empty" {
					query = "SELECT i,t FROM cursor_rows WHERE FALSE"
				}
				step(fixture.Exec("DECLARE h SCROLL CURSOR WITH HOLD FOR "+query, 0))
				fetch := func(direction string, indices ...int64) {
					step(sqlCursorBatch("FETCH "+direction+" FROM h", sqlCursorMotionRows(indices...), b.PG))
				}
				switch initial {
				case "on":
					fetch("2", 1, 2)
				case "last":
					fetch("3", 1, 2, 3)
				case "after":
					fetch("ALL", 1, 2, 3)
				case "empty":
					fetch("ALL")
				}
				// The held statement is described while the original producer lives;
				// execution resolves the promoted store after commit.
				q := "FETCH 0 FROM h"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				step(fixture.Exec("COMMIT", 0))
				var current []int64
				if initial == "on" {
					current = []int64{2}
				}
				if initial == "last" {
					current = []int64{3}
				}
				sqlCursorStep(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, sqlCursorBatch(q, sqlCursorMotionRows(current...), b.PG))
				switch initial {
				case "before":
					fetch("NEXT", 1)
				case "on":
					fetch("PRIOR", 1)
				case "last":
					fetch("PRIOR", 2)
				case "after":
					fetch("PRIOR", 3)
				case "empty":
					fetch("PRIOR")
					step(fixture.Exec("MOVE BACKWARD ALL FROM h", 0))
					fetch("NEXT")
					step(fixture.Exec("CLOSE h", 0))
					return
				}
				fetch("ABSOLUTE -1", 3)
				fetch("RELATIVE -1", 2)
				step(fixture.Exec("MOVE ABSOLUTE 0 FROM h", 0))
				fetch("NEXT", 1)
				step(fixture.Select("MOVE FORWARD ALL FROM h", []string{}, []string{}, [][]any{}))
				fetch("NEXT")
				step(fixture.Exec("MOVE ABSOLUTE 1 FROM h", 1))
				step(fixture.Exec("MOVE FORWARD ALL FROM h", 2))
				fetch("NEXT")
				fetch("PRIOR", 3)
				step(fixture.Exec("MOVE BACKWARD ALL FROM h", 2))
				fetch("NEXT", 1)
				step(fixture.Exec("BEGIN", 0))
				step(fixture.Exec("MOVE RELATIVE 1 FROM h", 1))
				step(fixture.Exec("ROLLBACK", 0))
				fetch("0", 2)
				step(fixture.Exec("CLOSE h", 0))
				step(fixture.ErrorDiagnostic("MOVE NEXT FROM h", sqlCursorMissing("h")))
			})
		})
	}
}

func TestReferenceSQLCursorScrollHoldCapturedValues(t *testing.T) {
	// REL_18_0 PersistHoldablePortal rewinds SCROLL before draining:
	// late targets redraw once at commit, then all stored motion uses those values.
	// Expected draws are independently source-derived, never native-derived.
	sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
		sqlCursorSetup(t, p, b, c)
		step(fixture.Exec("CREATE SEQUENCE scroll_hold_draw START 10", 0))
		step(fixture.Exec("BEGIN", 0))
		step(fixture.Exec("DECLARE h SCROLL CURSOR WITH HOLD FOR SELECT i,nextval('scroll_hold_draw') AS n FROM (SELECT i FROM cursor_rows ORDER BY i OFFSET 0) AS ordered", 0))
		state := func(n int64, called bool) {
			step(fixture.Select("SELECT last_value,is_called FROM scroll_hold_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
		}
		fetch := func(direction string, values ...int64) {
			var expected [][]any
			if len(values) != 0 {
				expected = [][]any{{values[0], values[1]}}
			}
			s := fixture.Select("FETCH "+direction+" FROM h", []string{"i", "n"}, []string{"INT4", "INT8"}, expected)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				fixture.DriverBasicMetadata(t, rows, b.PG, s.Types, []bool{false, false}, 1)
			}
			step(s)
		}
		state(10, false)
		fetch("NEXT", 1, 10)
		state(10, true)
		step(fixture.Exec("COMMIT", 0))
		state(13, true)
		fetch("0", 1, 11)
		state(13, true)
		fetch("ABSOLUTE -1", 3, 13)
		state(13, true)
		fetch("PRIOR", 2, 12)
		state(13, true)
		step(fixture.Exec("MOVE BACKWARD ALL FROM h", 1))
		state(13, true)
		fetch("NEXT", 1, 11)
		state(13, true)
		step(fixture.Exec("MOVE FORWARD ALL FROM h", 2))
		state(13, true)
		fetch("PRIOR", 3, 13)
		state(13, true)
		step(fixture.Exec("MOVE BACKWARD ALL FROM h", 2))
		state(13, true)
		fetch("0")
		state(13, true)
		fetch("NEXT", 1, 11)
		state(13, true)
		fetch("RELATIVE 1", 2, 12)
		state(13, true)
		step(fixture.Exec("MOVE FORWARD ALL FROM h", 1))
		state(13, true)
		fetch("NEXT")
		state(13, true)
		fetch("PRIOR", 3, 13)
		state(13, true)
		step(fixture.Exec("CLOSE h", 0))
	})
	for _, initial := range []string{"before", "after"} {
		t.Run(initial, func(t *testing.T) {
			sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
				sqlCursorSetup(t, p, b, c)
				step(fixture.Exec("CREATE SEQUENCE scroll_hold_draw START 10 CACHE 1", 0))
				step(fixture.Exec("BEGIN", 0))
				step(fixture.Exec("DECLARE h SCROLL CURSOR WITH HOLD FOR SELECT i,nextval('scroll_hold_draw') AS n FROM (SELECT i FROM cursor_rows ORDER BY i OFFSET 0) AS ordered", 0))
				fetch := func(direction string, values [][]any) {
					s := fixture.Select("FETCH "+direction+" FROM h", []string{"i", "n"}, []string{"INT4", "INT8"}, values)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						fixture.DriverBasicMetadata(t, rows, b.PG, s.Types, []bool{false, false}, 1)
					}
					step(s)
				}
				state := func(n int64, called bool) {
					step(fixture.Select("SELECT last_value,is_called FROM scroll_hold_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
				}
				state(10, false)
				if initial == "after" {
					fetch("ALL", [][]any{{int64(1), int64(10)}, {int64(2), int64(11)}, {int64(3), int64(12)}})
					state(12, true)
				}
				step(fixture.Exec("COMMIT", 0))
				last := int64(12)
				if initial == "after" {
					last = 15
				}
				state(last, true)
				fetch("0", nil)
				state(last, true)
				if initial == "before" {
					fetch("NEXT", [][]any{{int64(1), int64(10)}})
				} else {
					fetch("PRIOR", [][]any{{int64(3), int64(15)}})
				}
				state(last, true)
				step(fixture.Exec("CLOSE h", 0))
			})
		})
	}
}
