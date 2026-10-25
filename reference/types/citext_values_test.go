package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceCitextValues(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				citextInstall(t, p, b, c)
				run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE citext_values(id integer NOT NULL,v citext)", 0))
				run(fixture.Exec(citextLiteralInsert, 6))
				run(fixture.CitextSelect(b, "SELECT id,v FROM citext_values ORDER BY id", []string{"id", "v"}, citextLiteralRows()))
				var typedNil []byte
				inputs := []any{"Bound MiX", []byte("Bytes MiX"), "", []byte{}, nil, typedNil, int64(42)}
				want := [][]any{{int64(7), "Bound MiX"}, {int64(8), "Bytes MiX"}, {int64(9), ""}, {int64(10), ""}, {int64(11), nil}, {int64(12), nil}, {int64(13), "42"}}
				for i, input := range inputs {
					run(fixture.Exec("INSERT INTO citext_values VALUES ($1,$2)", 1, i+7, input))
					if bytes, ok := input.([]byte); ok && len(bytes) > 0 {
						bytes[0] = 'X'
					}
				}
				run(fixture.CitextSelect(b, "SELECT id,v FROM citext_values WHERE id>6 ORDER BY id", []string{"id", "v"}, want))
				run(fixture.CitextSelect(b, "SELECT NULL::citext AS v,CAST(NULL::text AS citext) AS n", []string{"v", "n"}, [][]any{{nil, nil}}))
			})
		})
	}
	t.Run("held_reuse", func(t *testing.T) {
		citextScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			citextInstall(t, p, b, c)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE citext_held(id integer,v citext)", 0))
			q := "INSERT INTO citext_held VALUES ($1,$2)"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for i, value := range []any{"First", nil, []byte("Last")} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, i+1, value))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.CitextSelect(b, "SELECT id,v FROM citext_held ORDER BY id", []string{"id", "v"}, [][]any{{int64(1), "First"}, {int64(2), nil}, {int64(3), "Last"}}))
			q = "SELECT $1::citext AS held"
			selectStmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer selectStmt.Close()
			held = fixture.HeldStatement{Executor: c, Statement: selectStmt, SQL: q}
			for _, value := range []any{"First", nil, "Last"} {
				s := fixture.CitextSelect(b, q, []string{"held"}, [][]any{{value}}, value)
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { citextMetadata(t, b.PG, rows, s.Columns, nil) }
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
	})
}
