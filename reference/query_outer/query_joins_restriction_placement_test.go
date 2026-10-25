package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// A WHERE condition that reads only one input of a join belongs to that input:
// it is evaluated for every row of the input, including rows without a join
// partner, and rows it rejects never reach a correlated right input.
func TestReferenceQueryJoinsWhereRestrictionPlacement(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			conn, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := conn.Close(); err != nil {
					t.Error(err)
				}
			})
			run := func(s fixture.Step) {
				t.Helper()
				fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s)
			}
			run(fixture.Exec("CREATE TABLE restrict_l(id INT, d INT)", 0))
			// The unmatched zero divisor comes first, so every plan reaches it.
			run(fixture.Exec("INSERT INTO restrict_l VALUES (2, 0), (1, 1), (3, 5)", 3))
			run(fixture.Exec("CREATE TABLE restrict_r(id INT, tag TEXT)", 0))
			run(fixture.Exec("INSERT INTO restrict_r VALUES (1, 'one'), (3, 'three'), (4, 'four')", 3))
			for _, prepared := range []bool{false, true} {
				step := func(s fixture.Step) {
					t.Helper()
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
				}
				divisionByZero := func(q string) {
					t.Helper()
					s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
					s.SelectRows = true
					s.ErrorStage = "execute"
					step(s)
				}
				ids := func(q string, want ...int64) {
					t.Helper()
					rows := make([][]any, len(want))
					for i, id := range want {
						rows[i] = []any{id}
					}
					step(fixture.Select(q, []string{"id"}, []string{"INT4"}, rows))
				}

				divisionByZero("SELECT l.id FROM restrict_l l JOIN restrict_r r ON r.id = l.id WHERE 10 / l.d > 0")
				divisionByZero("SELECT l.id FROM restrict_l l JOIN restrict_r r ON r.id = l.id JOIN restrict_r r2 ON r2.id = r.id WHERE r.tag <> 'none' AND 10 / l.d > 0")
				divisionByZero("SELECT l.id FROM restrict_l l LEFT JOIN restrict_r r ON r.id = l.id WHERE 10 / l.d > 0")
				step(fixture.Select("SELECT l.id, s.v FROM restrict_l l CROSS JOIN LATERAL generate_series(10 / l.d, 10 / l.d) s(v) WHERE l.d <> 0 ORDER BY l.id",
					[]string{"id", "v"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(3), int64(2)}}))
				step(fixture.Select("SELECT l.id, r.tag FROM restrict_l l JOIN restrict_r r ON r.id = l.id WHERE l.d >= $1 AND r.tag <> 'none' AND l.id + r.id > 1 AND random() >= 0 ORDER BY l.id",
					[]string{"id", "tag"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}, {int64(3), "three"}}, int64(1)))
				ids("SELECT l.id FROM restrict_l l JOIN restrict_r r ON r.id = l.id WHERE l.id = 3 OR l.d = 0 ORDER BY l.id", 3)
				ids("SELECT l.id FROM restrict_l l JOIN restrict_r r ON true WHERE r.id = 4 ORDER BY l.id", 1, 2, 3)
				ids("SELECT l.id FROM restrict_l l LEFT JOIN restrict_r r ON r.id = l.id WHERE r.id IS NULL", 2)
			}
		})
	}
}
