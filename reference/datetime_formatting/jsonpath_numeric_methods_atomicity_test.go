package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathNumericMethodsAtomicity(t *testing.T) {
	for _, tc := range []struct{ path, good, bad, want, code, message string }{
		{`$.number()`, `1.2300`, `"bad"`, `[1.2300]`, "22036", numericMethodsInvalid("number", "bad")},
		{`$.double()`, `9007199254740993`, `"Infinity"`, `[9007199254740993]`, "22036", `NaN or Infinity is not allowed for jsonpath item method .double()`},
		{`$.double()`, `"1.25"`, `"1e1000"`, `[1.25]`, "22036", numericMethodsInvalid("double", "1e1000")},
		{`$.abs()`, `-1.25`, `"1"`, `[1.25]`, "22036", numericMethodsKind("abs")},
		{`$.floor()`, `-1.25`, `null`, `[-2]`, "22036", numericMethodsKind("floor")},
		{`$.ceiling()`, `1.25`, `true`, `[2]`, "22036", numericMethodsKind("ceiling")},
	} {
		t.Run(tc.path, func(t *testing.T) {
			jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TABLE jpnm_source(id int PRIMARY KEY,doc jsonb,path jsonpath,result jsonb DEFAULT '99')`, 0))
				run(fixture.Exec(`INSERT INTO jpnm_source(id,doc,path) VALUES(1,$1,$3),(2,$2,$3)`, 2, tc.good, tc.bad, tc.path))
				run(fixture.Exec(`CREATE TABLE jpnm_out(id int PRIMARY KEY,v jsonb)`, 0))
				run(fixture.Exec(`INSERT INTO jpnm_out VALUES(0,'99')`, 1))
				var baseline bytes.Buffer
				if !b.PG {
					if err:=stuntgres.Dump(p.Ctx,p.Native,&baseline);err!=nil {t.Fatal(err)}
				}
				unchanged := func() {
					run(fixture.Select(`SELECT id,v FROM jpnm_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}}))
					if !b.PG {
						var after bytes.Buffer
						if err:=stuntgres.Dump(p.Ctx,p.Native,&after);err!=nil {t.Fatal(err)}
						if !bytes.Equal(baseline.Bytes(),after.Bytes()) {t.Fatal("failed DML changed dump")}
					}
				}
				for i, q := range []string{
					`INSERT INTO jpnm_out SELECT id,jsonb_path_query_array(doc,path) FROM jpnm_source ORDER BY id`,
					`INSERT INTO jpnm_out VALUES(1,jsonb_path_query_array($1,$3)),(2,jsonb_path_query_array($2,$3))`,
				} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message))
						if i == 1 {
							s.Args = []any{tc.good, tc.bad, tc.path}
						}
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
						unchanged()
					}
				}
				q := `UPDATE jpnm_source SET result=jsonb_path_query_array(doc,path,'{}',$1)`
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), false))
				run(fixture.Select(`SELECT id,result FROM jpnm_source ORDER BY id`, []string{"id", "result"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `99`}, {int64(2), `99`}}))
				unchanged()
				run(fixture.Exec(`UPDATE jpnm_source SET doc=$1 WHERE id=2`, 1, tc.good))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, false))
				run(fixture.Select(`SELECT id,result FROM jpnm_source ORDER BY id`, []string{"id", "result"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), tc.want}, {int64(2), tc.want}}))
			})
		})
	}
}
