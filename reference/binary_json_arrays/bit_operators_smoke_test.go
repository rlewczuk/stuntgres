package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitOperatorsFeatureSmoke(t *testing.T) {
	bitOperatorsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE bop_smoke(id INTEGER,f "bit",v VARBIT)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO bop_smoke VALUES(1,B'10001',B'01101'),(2,NULL,NULL)", 2))
		const q = "SELECT f||v AS cat,f&$1 AS a,f|$1 AS o,f#$1 AS x,~f AS n,f<<3 AS l,f>>2 AS r,f=$1 AS eq,f<>$1 AS ne,f<$1 AS lt,f<=$1 AS le,f>$1 AS gt,f>=$1 AS ge FROM bop_smoke WHERE id=$2"
		names := []string{"cat", "a", "o", "x", "n", "l", "r", "eq", "ne", "lt", "le", "gt", "ge"}
		types := []string{"VARBIT", "BIT", "BIT", "BIT", "BIT", "BIT", "BIT", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}
		good := [][]any{{"1000101101", "00001", "11101", "11100", "01110", "01000", "00100", false, true, false, false, true, true}}
		step := func(bits any, id int, rows [][]any) fixture.Step {
			s := fixture.Select(q, names, types, rows, bits, id)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				cols, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				bitOperatorsMetadata(t, cols, names, types)
			}
			return s
		}
		bitOperatorsBoth(t, p, b, c, step("01101", 1, good))
		bitOperatorsBoth(t, p, b, c, step("01101", 2, [][]any{{nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil}}))
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step("01101", 1, good))
		d := fixture.ControlsReferenceDiagnostic("22026", "cannot AND bit strings of different sizes")
		bad := fixture.ErrorDiagnostic(q, d, "1", 1)
		bad.SelectRows, bad.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, step("01101", 1, good))
		bitOperatorsBoth(t, p, b, c, fixture.Select("SELECT B'1'>B'01' AS zeros,B'1'<B'10' AS prefix,B'10'<>B'100' AS width,1<<32 AS integral", []string{"zeros", "prefix", "width", "integral"}, []string{"BOOL", "BOOL", "BOOL", "INT4"}, [][]any{{true, true, true, int64(1)}}))
	})
}
