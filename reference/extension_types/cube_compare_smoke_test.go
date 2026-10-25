package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCubeComparisonFeatureSmoke(t *testing.T) {
	cubeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cubeInstall(t, p, b, c)
		cubeComparisonTable(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE INDEX cube_compare_smoke_idx ON cube_compare_data(v)", 0))
		q := "SELECT " + cubeComparisonProjection("$1::cube", "$2::cube", false)
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			left, right any
			row         []any
		}{
			{"(3,1),(1,4)", []byte("(1,1),(3,4)"), cubeComparisonRow(0)},
			{"(2)", "(1,100)", cubeComparisonRow(1)},
			{[]byte("NaN"), "0", cubeComparisonRow(0)},
			{nil, "0", []any{nil, nil, nil, nil, nil, nil, nil}},
		} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, cubeComparisonStep(q, tc.row, tc.left, tc.right))
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		cubeComparisonHeldConsumer(t, p, b, c)
		run(cubeComparisonIDs("SELECT id FROM cube_compare_data ORDER BY v,id", 4, 6, 7, 2, 3, 1, 5))
		run(fixture.Exec("CREATE TABLE cube_compare_smoke_unique(id integer,v cube)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX cube_compare_smoke_key ON cube_compare_smoke_unique(v)", 0))
		run(fixture.Exec("INSERT INTO cube_compare_smoke_unique VALUES (1,'(3,1),(1,4)')", 1))
		want := cubeComparisonUniqueDiagnostic("cube_compare_smoke_unique", "cube_compare_smoke_key", "(1, 1),(3, 4)")
		cubeError(t, p, b, c, "INSERT INTO cube_compare_smoke_unique VALUES (2,$1)", true, false, false, want, []byte("(1,1),(3,4)"))
		run(cubeComparisonIDs("SELECT id FROM cube_compare_smoke_unique ORDER BY id", 1))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		} else {
			for _, q := range []string{"DROP TABLE cube_compare_data", "DROP TABLE cube_compare_smoke_unique", "DROP EXTENSION cube"} {
				run(fixture.Exec(q, 0))
			}
			cubeInstall(t, p, b, c)
			cubeComparisonTable(t, p, b, c)
			run(fixture.Exec("CREATE INDEX cube_compare_smoke_idx ON cube_compare_data(v)", 0))
			run(fixture.Exec("CREATE TABLE cube_compare_smoke_unique(id integer,v cube)", 0))
			run(fixture.Exec("CREATE UNIQUE INDEX cube_compare_smoke_key ON cube_compare_smoke_unique(v)", 0))
			run(fixture.Exec("INSERT INTO cube_compare_smoke_unique VALUES (1,'(3,1),(1,4)')", 1))
		}
		cubeComparisonHeldConsumer(t, p, b, c)
		run(cubeComparisonIDs("SELECT id FROM cube_compare_data WHERE v>='(0)'::cube ORDER BY v,id", 6, 7, 2, 3, 1))
		cubeError(t, p, b, c, "INSERT INTO cube_compare_smoke_unique VALUES (2,'(1,1),(3,4)')", false, false, false, want)
		run(cubeComparisonIDs("SELECT id FROM cube_compare_smoke_unique ORDER BY id", 1))
	})
}
