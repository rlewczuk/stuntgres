package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONPathIntegerDecimalAtomicity(t *testing.T) {
	for _, tc := range []struct{ path, good, bad, want, code, message string }{
		{`$.integer()`, `1.5`, `"bad"`, `[2]`, "22036", integerDecimalInvalid("integer", "bad")},
		{`$.bigint()`, `9223372036854775807`, `9223372036854775808`, `[9223372036854775807]`, "22036", integerDecimalInvalid("bigint", "9223372036854775808")},
		{`$.decimal(3,1)`, `1.25`, `99.95`, `[1.3]`, "22036", integerDecimalInvalid("decimal", "99.95")},
		{`$.decimal(0)`, `[]`, `1`, `[]`, "22023", `NUMERIC precision 0 must be between 1 and 1000`},
	} {
		t.Run(tc.path, func(t *testing.T) {
			jsonpathNavigateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
				run(fixture.Exec(`CREATE TABLE jpid_source(id int PRIMARY KEY,doc jsonb,path jsonpath,result jsonb DEFAULT '99')`, 0))
				run(fixture.Exec(`INSERT INTO jpid_source(id,doc,path) VALUES(1,$1,$3),(2,$2,$3)`, 2, tc.good, tc.bad, tc.path))
				run(fixture.Exec(`CREATE TABLE jpid_out(id int PRIMARY KEY,v jsonb)`, 0))
				run(fixture.Exec(`INSERT INTO jpid_out VALUES(0,'99')`, 1))
				unchanged := func() {
					run(fixture.Select(`SELECT id,v FROM jpid_out ORDER BY id`, []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(0), `99`}}))
				}
				for i, q := range []string{
					`INSERT INTO jpid_out SELECT id,jsonb_path_query_array(doc,path) FROM jpid_source ORDER BY id`,
					`INSERT INTO jpid_out VALUES(1,jsonb_path_query_array($1,$3)),(2,jsonb_path_query_array($2,$3))`,
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
				q := `UPDATE jpid_source SET result=jsonb_path_query_array(doc,path,'{}',$1)`
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), false))
				run(fixture.Select(`SELECT id,result FROM jpid_source ORDER BY id`, []string{"id", "result"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `99`}, {int64(2), `99`}}))
				run(fixture.Exec(`UPDATE jpid_source SET doc=$1 WHERE id=2`, 1, tc.good))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, false))
				run(fixture.Select(`SELECT id,result FROM jpid_source ORDER BY id`, []string{"id", "result"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), tc.want}, {int64(2), tc.want}}))
			})
		})
	}
}
