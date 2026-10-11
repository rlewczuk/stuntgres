package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSmallintArithmeticParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE t(id INT,s SMALLINT,i INT,b BIGINT)", 0))
			run(fixture.Exec("INSERT INTO t VALUES(1,7,2,2),(2,-7,-2,-2),(3,NULL,NULL,NULL)", 3))
			for _, tc := range []struct {
				q          string
				types      []string
				two, three [][]any
			}{
				{"SELECT s+$1,i+$1,b+$1 FROM t ORDER BY id", []string{"INT2", "INT4", "INT8"}, [][]any{{int64(9), int64(4), int64(4)}, {int64(-5), int64(0), int64(0)}, {nil, nil, nil}}, [][]any{{int64(10), int64(5), int64(5)}, {int64(-4), int64(1), int64(1)}, {nil, nil, nil}}},
				{"SELECT i+$1,s+$1 FROM t ORDER BY id", []string{"INT4", "INT4"}, [][]any{{int64(4), int64(9)}, {int64(0), int64(-5)}, {nil, nil}}, [][]any{{int64(5), int64(10)}, {int64(1), int64(-4)}, {nil, nil}}},
				{"SELECT b+$1,s+$1 FROM t ORDER BY id", []string{"INT8", "INT8"}, [][]any{{int64(4), int64(9)}, {int64(0), int64(-5)}, {nil, nil}}, [][]any{{int64(5), int64(10)}, {int64(1), int64(-4)}, {nil, nil}}},
				{"SELECT s%$1,i%$1,b%$1 FROM t ORDER BY id", []string{"INT2", "INT4", "INT8"}, [][]any{{int64(1), int64(0), int64(0)}, {int64(-1), int64(0), int64(0)}, {nil, nil, nil}}, [][]any{{int64(1), int64(2), int64(2)}, {int64(-1), int64(-2), int64(-2)}, {nil, nil, nil}}},
				{"SELECT i%$1,s%$1 FROM t ORDER BY id", []string{"INT4", "INT4"}, [][]any{{int64(0), int64(1)}, {int64(0), int64(-1)}, {nil, nil}}, [][]any{{int64(2), int64(1)}, {int64(-2), int64(-1)}, {nil, nil}}},
				{"SELECT b%$1,s%$1 FROM t ORDER BY id", []string{"INT8", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(0), int64(-1)}, {nil, nil}}, [][]any{{int64(2), int64(1)}, {int64(-2), int64(-1)}, {nil, nil}}},
			} {
				stmt, err := backend.DB.PrepareContext(p.Ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{backend.DB, stmt, tc.q}
				columns := []string{"?column?", "?column?"}
				if len(tc.types) == 3 {
					columns = append(columns, "?column?")
				}
				nullRow := make([]any, len(tc.types))
				for _, frame := range []struct {
					arg  any
					rows [][]any
				}{{int64(2), tc.two}, {nil, [][]any{nullRow, nullRow, nullRow}}, {int64(3), tc.three}} {
					s := fixture.Select(tc.q, columns, tc.types, frame.rows, frame.arg)
					run(s)
					fixture.RunStep(t, p.Ctx, referenceArithmeticMetadataExecutor{held, t, backend.PG, tc.types}, backend.Recorder, s)
				}
				if tc.types[0] == "INT2" {
					for _, bad := range []struct {
						arg           any
						code, message string
					}{{int64(32768), "22003", `value "32768" is out of range for type smallint`}, {"oops", "22P02", `invalid input syntax for type smallint: "oops"`}, {"", "22P02", `invalid input syntax for type smallint: ""`}} {
						context := "unnamed portal parameter $1 = '...'"
						if bad.arg == "" {
							context = "unnamed portal parameter $1 = ''"
						}
						s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: bad.code, Message: bad.message, Context: context}, bad.arg)
						s.SelectRows = true
						s.ErrorStage = "execute"
						run(s)
						fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
					}
				}
				if err := stmt.Close(); err != nil {
					t.Fatal(err)
				}
			}
			s := fixture.ImplicitError("SELECT $1,s+$1 FROM t", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "smallint versus text", Position: "8"}, 2)
			s.SelectRows = true
			run(s)
			s.ErrorStage = "prepare"
			run(fixture.Prepared(s))
		})
	}
}
