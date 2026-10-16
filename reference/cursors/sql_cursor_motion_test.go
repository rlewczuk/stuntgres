package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func sqlCursorMotionScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn, func(fixture.Step))) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			sqlCursorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				step := func(s fixture.Step) {
					if prepared {
						s = fixture.Prepared(s)
					}
					sqlCursorStep(t, p, b, c, s)
				}
				body(t, p, b, c, step)
			})
		})
	}
}

func sqlCursorMotionRows(indices ...int64) [][]any {
	rows := make([][]any, 0, len(indices))
	for _, i := range indices {
		var text any
		if i == 1 {
			text = "one"
		}
		if i == 3 {
			text = "three"
		}
		rows = append(rows, []any{i, text})
	}
	return rows
}

func sqlCursorMotionStart(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
	t.Helper()
	sqlCursorSetup(t, p, b, c)
	step(fixture.Exec("BEGIN", 0))
	step(fixture.Exec("DECLARE c SCROLL CURSOR FOR SELECT i,t FROM cursor_rows ORDER BY i", 0))
}

func TestReferenceSQLCursorScrollDirections(t *testing.T) {
	sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
		sqlCursorMotionStart(t, p, b, c, step)
		for _, probe := range []struct {
			direction string
			rows      []int64
		}{
			{"", []int64{1}}, {"NEXT", []int64{2}}, {"FORWARD", []int64{3}},
			{"PRIOR", []int64{2}}, {"BACKWARD", []int64{1}}, {"FIRST", []int64{1}},
			{"LAST", []int64{3}}, {"FORWARD -2", []int64{2, 1}}, {"BACKWARD -2", []int64{2, 3}},
			{"ABSOLUTE 1", []int64{1}}, {"ABSOLUTE -1", []int64{3}}, {"ABSOLUTE -3", []int64{1}},
			{"ABSOLUTE -4", nil}, {"NEXT", []int64{1}}, {"ABSOLUTE 4", nil}, {"PRIOR", []int64{3}},
			{"ABSOLUTE 0", nil}, {"RELATIVE 2", []int64{2}}, {"RELATIVE 0", []int64{2}},
			{"RELATIVE 1", []int64{3}}, {"RELATIVE 1", nil}, {"RELATIVE -2", []int64{2}},
			{"+1", []int64{3}}, {"-2", []int64{2, 1}}, {"ALL", []int64{2, 3}},
			{"BACKWARD ALL", []int64{3, 2, 1}}, {"FORWARD 2147483647", []int64{1, 2, 3}},
			{"FORWARD -2147483647", []int64{3, 2, 1}},
		} {
			step(sqlCursorBatch("FETCH "+probe.direction+" FROM c", sqlCursorMotionRows(probe.rows...), b.PG))
		}
	})
}

func TestReferenceSQLCursorScrollBoundaries(t *testing.T) {
	for _, trace := range [][]struct {
		direction string
		rows      []int64
	}{
		{{"2", []int64{1, 2}}, {"PRIOR", []int64{1}}, {"PRIOR", nil}, {"NEXT", []int64{1}}},
		{{"3", []int64{1, 2, 3}}, {"FORWARD 0", []int64{3}}, {"NEXT", nil}, {"BACKWARD 0", nil}, {"PRIOR", []int64{3}}, {"BACKWARD ALL", []int64{2, 1}}, {"NEXT", []int64{1}}},
		{{"ALL", []int64{1, 2, 3}}, {"NEXT", nil}, {"NEXT", nil}, {"BACKWARD ALL", []int64{3, 2, 1}}, {"PRIOR", nil}, {"PRIOR", nil}, {"NEXT", []int64{1}}},
	} {
		t.Run(trace[0].direction, func(t *testing.T) {
			sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
				sqlCursorMotionStart(t, p, b, c, step)
				for _, probe := range trace {
					step(sqlCursorBatch("FETCH "+probe.direction+" FROM c", sqlCursorMotionRows(probe.rows...), b.PG))
				}
			})
		})
	}
}

func TestReferenceSQLCursorScrollMetadataAndOccurrences(t *testing.T) {
	sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
		sqlCursorMotionStart(t, p, b, c, step)
		for _, predicate := range []string{"FALSE", "i=2"} {
			step(fixture.Exec("DECLARE small SCROLL CURSOR FOR SELECT i,t FROM cursor_rows WHERE "+predicate, 0))
			var rows [][]any
			if predicate == "i=2" {
				rows = sqlCursorMotionRows(2)
			} else {
				rows = sqlCursorMotionRows()
			}
			step(sqlCursorBatch("FETCH 1 FROM small", rows, b.PG))
			step(sqlCursorBatch("FETCH 0 FROM small", rows, b.PG))
			step(sqlCursorBatch("FETCH NEXT FROM small", sqlCursorMotionRows(), b.PG))
			step(sqlCursorBatch("FETCH PRIOR FROM small", rows, b.PG))
			step(fixture.Exec("CLOSE small", 0))
		}
		step(fixture.Exec("DECLARE zero_width SCROLL CURSOR FOR SELECT FROM cursor_rows", 0))
		step(fixture.Select("FETCH ALL FROM zero_width", []string{}, []string{}, [][]any{{}, {}, {}}))
		step(fixture.Exec("MOVE BACKWARD 2 FROM zero_width", 2))
		step(fixture.Select("FETCH 0 FROM zero_width", []string{}, []string{}, [][]any{{}}))
		step(fixture.Select("FETCH BACKWARD ALL FROM zero_width", []string{}, []string{}, [][]any{{}}))
		step(fixture.Exec("DECLARE duplicates SCROLL CURSOR FOR VALUES (NULL::INTEGER),(1),(1)", 0))
		step(fixture.Select("FETCH ALL FROM duplicates", []string{"column1"}, []string{"INT4"}, [][]any{{nil}, {int64(1)}, {int64(1)}}))
		step(fixture.Select("FETCH BACKWARD ALL FROM duplicates", []string{"column1"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(1)}, {nil}}))
	})
}

func sqlCursorMoveTrace(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
	t.Helper()
	sqlCursorMotionStart(t, p, b, c, step)
	step(fixture.Exec("MOVE FORWARD 2 FROM c", 2))
	step(sqlCursorBatch("FETCH 0 FROM c", sqlCursorMotionRows(2), b.PG))
	step(sqlCursorBatch("FETCH NEXT FROM c", sqlCursorMotionRows(3), b.PG))
	step(sqlCursorBatch("FETCH 0 FROM c", sqlCursorMotionRows(3), b.PG))
	step(fixture.Exec("MOVE NEXT FROM c", 0))
	step(sqlCursorBatch("FETCH BACKWARD 0 FROM c", sqlCursorMotionRows(), b.PG))
	step(fixture.Exec("MOVE PRIOR FROM c", 1))
	step(fixture.Exec("MOVE 0 FROM c", 1))
	step(sqlCursorBatch("FETCH 0 FROM c", sqlCursorMotionRows(3), b.PG))
	step(fixture.Exec("MOVE BACKWARD ALL FROM c", 2))
	step(sqlCursorBatch("FETCH NEXT FROM c", sqlCursorMotionRows(1), b.PG))
	step(fixture.Exec("MOVE ABSOLUTE 2 FROM c", 1))
	step(sqlCursorBatch("FETCH 0 FROM c", sqlCursorMotionRows(2), b.PG))
	step(fixture.Exec("MOVE RELATIVE 99 FROM c", 0))
	step(sqlCursorBatch("FETCH PRIOR FROM c", sqlCursorMotionRows(3), b.PG))
	step(sqlCursorBatch("FETCH ABSOLUTE -1 FROM c", sqlCursorMotionRows(3), b.PG))
	step(fixture.Exec("MOVE BACKWARD ALL FROM c", 2))
	step(sqlCursorBatch("FETCH NEXT FROM c", sqlCursorMotionRows(1), b.PG))
	step(fixture.Exec("MOVE RELATIVE 1 FROM c", 1))
	step(sqlCursorBatch("FETCH 0 FROM c", sqlCursorMotionRows(2), b.PG))
}

func TestReferenceSQLCursorMoveCountsAndPreparation(t *testing.T) {
	sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
		sqlCursorMoveTrace(t, p, b, c, step)
		step(fixture.Select("MOVE NEXT FROM c", []string{}, []string{}, [][]any{}))
		step(sqlCursorBatch("FETCH 0 FROM c", sqlCursorMotionRows(3), b.PG))
		query := "MOVE NEXT FROM absent"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal("quiet MOVE Prepare", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		sqlCursorError(t, p, b, held, query, sqlCursorMissing("absent"))
		step(fixture.Exec("DECLARE absent SCROLL CURSOR FOR SELECT i FROM cursor_rows ORDER BY i", 0))
		sqlCursorStep(t, p, b, held, fixture.Exec(query, 1))
		step(fixture.Exec("CLOSE absent", 0))
		step(fixture.Exec("DECLARE absent SCROLL CURSOR FOR SELECT t FROM cursor_rows ORDER BY i", 0))
		sqlCursorStep(t, p, b, held, fixture.Exec(query, 1))
	})
}

func TestReferenceSQLCursorMoveFeatureSmoke(t *testing.T) {
	sqlCursorMotionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, step func(fixture.Step)) {
		sqlCursorMoveTrace(t, p, b, c, step)
		rows, err := c.QueryContext(p.Ctx, "FETCH PRIOR FROM c")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		readBatch := func() {
			if !rows.Next() {
				t.Fatal("prior batch", rows.Err())
			}
			var i int64
			var text string
			if err := rows.Scan(&i, &text); err != nil {
				t.Fatal(err)
			}
			if i != 1 || text != "one" {
				t.Fatalf("prior row=%d,%q", i, text)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("batch tail", rows.Err())
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
		// pq owns an active wire response until drained; native batches are
		// detached and independently remain readable after CLOSE and COMMIT.
		if b.PG {
			readBatch()
		}
		step(fixture.Exec("CLOSE c", 0))
		step(fixture.Exec("COMMIT", 0))
		if !b.PG {
			readBatch()
		}
		step(fixture.ErrorDiagnostic("MOVE NEXT FROM c", sqlCursorMissing("c")))
	})
}
