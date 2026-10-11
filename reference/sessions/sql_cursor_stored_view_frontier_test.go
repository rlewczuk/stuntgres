package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLCursorStoredViewFrontier(t *testing.T) {
	sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
		sqlCursorSetup(t, p, b, c)
		step(fixture.Exec("CREATE VIEW motion_cached_view AS SELECT i FROM cursor_rows ORDER BY i", 0))
		step(fixture.Exec("BEGIN", 0))
		// No outer sort: MOVE captures only a prefix of the stored-view bridge.
		step(fixture.Exec("DECLARE c SCROLL CURSOR FOR SELECT i FROM motion_cached_view", 0))
		fetch := func(direction string, indices ...int64) {
			values := make([][]any, 0, len(indices))
			for _, i := range indices {
				values = append(values, []any{i})
			}
			s := fixture.Select("FETCH "+direction+" FROM c", []string{"i"}, []string{"INT4"}, values)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				fixture.DriverBasicMetadata(t, rows, b.PG, s.Types, []bool{true})
			}
			step(s)
		}
		step(fixture.Exec("MOVE NEXT FROM c", 1))
		step(fixture.Exec("MOVE ABSOLUTE 0 FROM c", 0))
		fetch("ALL", 1, 2, 3)
		fetch("ABSOLUTE -1", 3)
		fetch("BACKWARD ALL", 2, 1)
		step(fixture.Exec("MOVE FORWARD ALL FROM c", 3))
		fetch("NEXT")
		step(fixture.Exec("MOVE BACKWARD ALL FROM c", 3))
		fetch("NEXT", 1)
		step(fixture.Exec("MOVE ABSOLUTE 3 FROM c", 1))
		fetch("0", 3)
		step(fixture.Exec("MOVE NEXT FROM c", 0))
		fetch("PRIOR", 3)
		step(fixture.Exec("CLOSE c", 0))
		step(fixture.Exec("ROLLBACK", 0))
	})
}

func TestReferenceSQLCursorScrollHoldStoredViewFrontier(t *testing.T) {
	for _, captured := range []bool{false, true} {
		name := "plain"
		if captured {
			name = "captured"
		}
		t.Run(name, func(t *testing.T) {
			sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
				sqlCursorSetup(t, p, b, c)
				step(fixture.Exec("CREATE VIEW motion_cached_view AS SELECT i FROM cursor_rows ORDER BY i", 0))
				query := "SELECT i FROM motion_cached_view"
				if captured {
					step(fixture.Exec("CREATE SEQUENCE view_hold_draw START 10 CACHE 1", 0))
					query = "SELECT i,nextval('view_hold_draw') AS n FROM motion_cached_view"
				}
				state := func(n int64, called bool) {
					if captured {
						step(fixture.Select("SELECT last_value,is_called FROM view_hold_draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{n, called}}))
					}
				}
				fetch := func(direction string, indices ...int64) {
					columns, types := []string{"i"}, []string{"INT4"}
					nullable := []bool{true}
					var computed []int
					if captured {
						columns, types = []string{"i", "n"}, []string{"INT4", "INT8"}
						nullable, computed = []bool{true, false}, []int{1}
					}
					values := make([][]any, 0, len(indices))
					for _, i := range indices {
						row := []any{i}
						if captured {
							// MOVE drew 10; commit's full three-row capture draws 11..13.
							row = append(row, i+10)
						}
						values = append(values, row)
					}
					s := fixture.Select("FETCH "+direction+" FROM h", columns, types, values)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						fixture.DriverBasicMetadata(t, rows, b.PG, s.Types, nullable, computed...)
					}
					step(s)
					state(13, true)
				}
				step(fixture.Exec("BEGIN", 0))
				step(fixture.Exec("DECLARE h SCROLL CURSOR WITH HOLD FOR "+query, 0))
				state(10, false)
				step(fixture.Exec("MOVE NEXT FROM h", 1))
				state(10, true)
				step(fixture.Exec("COMMIT", 0))
				state(13, true)
				fetch("0", 1)
				step(fixture.Exec("MOVE ABSOLUTE 0 FROM h", 0))
				fetch("ALL", 1, 2, 3)
				fetch("BACKWARD ALL", 3, 2, 1)
				step(fixture.Exec("MOVE FORWARD ALL FROM h", 3))
				state(13, true)
				fetch("NEXT")
				fetch("ABSOLUTE -1", 3)
				fetch("RELATIVE -1", 2)
				step(fixture.Exec("MOVE BACKWARD ALL FROM h", 1))
				state(13, true)
				fetch("NEXT", 1)
				step(fixture.Exec("MOVE ABSOLUTE 3 FROM h", 1))
				fetch("0", 3)
				step(fixture.Exec("MOVE FORWARD ALL FROM h", 0))
				fetch("PRIOR", 3)
				step(fixture.Exec("MOVE BACKWARD ALL FROM h", 2))
				state(13, true)
				fetch("NEXT", 1)
				step(fixture.Exec("CLOSE h", 0))
			})
		})
	}
}
