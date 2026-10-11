package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceNumericLiteralsPublic(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(s fixture.Step) {
						s.Prepared = prepared
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					check := func(s fixture.Step) {
						run(s)
						rows, err := backend.DB.QueryContext(p.Ctx, s.Query)
						if err != nil {
							t.Fatal(err)
						}
						fixture.LexicalRows(t, backend.PG, rows, s, nil)
					}
					run(fixture.Exec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
					run(fixture.Exec("INSERT INTO t VALUES (1,0b1010,0o12),(2,0xa,1_000)", 2))
					check(fixture.Select("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}}))
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO t VALUES ($1,0B_1_010,0X_A)", 1, int64(3))))
					check(fixture.Select("SELECT id,i,b FROM t WHERE i=0b1010 ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}, {int64(3), int64(10), int64(10)}}))
					check(fixture.Select("SELECT id,i,b FROM t WHERE b=1_000", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(10), int64(1000)}}))
					run(fixture.Exec("CREATE TABLE endpoints(id INT,b BIGINT)", 0))
					var want [][]any
					for i, literal := range numericEndpoints() {
						run(fixture.Exec(fmt.Sprintf("INSERT INTO endpoints VALUES (%d,%s)", i, literal), 1))
						value := int64(9223372036854775807)
						if i%2 == 1 {
							value = -9223372036854775808
						}
						want = append(want, []any{int64(i), value})
					}
					check(fixture.Select("SELECT id,b FROM endpoints ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, want))
					run(fixture.Exec("CREATE TABLE bounds(i INT,b BIGINT,label TEXT)", 0))
					run(fixture.Exec("INSERT INTO bounds VALUES (-0x8000_0000,0x8000_0000,0xa),(0x7FFF_FFFF,010,1_000)", 2))
					check(fixture.Select("SELECT i,b,label FROM bounds ORDER BY i", []string{"i", "b", "label"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(-2147483648), int64(2147483648), "10"}, {int64(2147483647), int64(10), "1000"}}))
				})
			}
		})
	}
}

func TestReferenceNumericLiteralsSpellings(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := fixture.NewScenario(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(id INT,b BIGINT)", 0))
			var want [][]any
			for i, tc := range numericLiteralSpellings() {
				s := fixture.Exec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s)", i, tc.literal), 1)
				s.Prepared = prepared
				p.Step(t, s)
				want = append(want, []any{int64(i), tc.value})
			}
			s := fixture.Select("SELECT id,b FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, want)
			p.Step(t, s)
			for _, backend := range p.Backends() {
				rows, err := backend.DB.QueryContext(p.Ctx, s.Query)
				if err != nil {
					t.Fatal(err)
				}
				fixture.LexicalRows(t, backend.PG, rows, s, nil)
			}
		})
	}
}
