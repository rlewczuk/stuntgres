package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLCursorScrollSystemIdentity(t *testing.T) {
	sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
		step(fixture.Exec("CREATE TABLE cursor_identity_rows(i INTEGER NOT NULL)", 0))
		step(fixture.Exec("INSERT INTO cursor_identity_rows VALUES(3),(1),(2)", 3))
		step(fixture.Exec("CREATE TABLE cursor_virtual_identity(i INTEGER NOT NULL,n INTEGER GENERATED ALWAYS AS(i+1) VIRTUAL)", 0))
		step(fixture.Exec("INSERT INTO cursor_virtual_identity(i) VALUES(1),(2)", 2))
		step(fixture.Exec("BEGIN", 0))

		// Existing bounded virtual compound projection must retain the selected
		// occurrence's absent/present frame identity, not the next-demand frontier.
		// No ctid provider is assumed: only integrated generated/join semantics.
		joined := "SELECT l.i,g.n+1 AS n FROM cursor_identity_rows AS l LEFT JOIN cursor_virtual_identity AS g ON l.i=g.i ORDER BY l.i LIMIT 3 OFFSET 0"
		plain := "SELECT i,n+1 AS n FROM cursor_virtual_identity ORDER BY i LIMIT 3 OFFSET 0"
		batch := func(query string, indices []int64, absentLast bool) fixture.Step {
			want := make([][]any, 0, len(indices))
			for _, i := range indices {
				var n any = i + 2
				if absentLast && i == 3 {
					n = nil
				}
				want = append(want, []any{i, n})
			}
			s := fixture.Select(query, []string{"i", "n"}, []string{"INT4", "INT4"}, want)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				fixture.DriverBasicMetadata(t, rows, b.PG, s.Types, []bool{false, true}, 1)
			}
			return s
		}
		for _, absentLast := range []bool{true, false} {
			query := joined
			if !absentLast {
				// The absent/present reverse gate above completes before this
				// all-present independently expected compound sequence 3,4,5.
				step(fixture.Exec("INSERT INTO cursor_virtual_identity(i) VALUES(3)", 1))
				query = plain
			}
			step(batch(query, []int64{1, 2, 3}, absentLast))
			step(fixture.Exec("DECLARE identity_cursor SCROLL CURSOR FOR "+query, 0))
			for _, probe := range []struct {
				direction string
				indices   []int64
			}{
				{"NEXT", []int64{1}},
				{"ABSOLUTE 2", []int64{2}},
				// First reverse seek is before EOF. Selected i=1 is present;
				// stale next-demand i=3 is absent. A frame mismatch fails here
				// without reaching an inferred EOF indexing panic. No recovery.
				{"PRIOR", []int64{1}},
				{"RELATIVE 1", []int64{2}},
				{"RELATIVE -1", []int64{1}},
				{"ABSOLUTE 3", []int64{3}}, // exact finite last, not after-last
				{"FORWARD 0", []int64{3}},
				{"NEXT", nil}, // now after EOF
				{"PRIOR", []int64{3}},
				{"ABSOLUTE -2", []int64{2}},
				{"RELATIVE -1", []int64{1}},
				{"ABSOLUTE 0", nil},
				{"NEXT", []int64{1}},
				{"ALL", []int64{2, 3}},
				{"BACKWARD ALL", []int64{3, 2, 1}},
				{"NEXT", []int64{1}},
			} {
				step(batch("FETCH "+probe.direction+" FROM identity_cursor", probe.indices, absentLast))
			}
			step(fixture.Exec("CLOSE identity_cursor", 0))
		}
		step(fixture.Exec("ROLLBACK", 0))
	})
}
