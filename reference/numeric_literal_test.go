package reference_test

import (
	"fmt"
	"testing"
)

func TestReferenceNumericLiteralsPublic(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) {
						s.prepared = prepared
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					}
					check := func(s referenceStep) {
						run(s)
						rows, err := backend.db.QueryContext(p.ctx, s.query)
						if err != nil {
							t.Fatal(err)
						}
						lexicalRows(t, backend.pg, rows, s, nil)
					}
					run(referenceExec("CREATE TABLE t(id INT,i INT,b BIGINT)", 0))
					run(referenceExec("INSERT INTO t VALUES (1,0b1010,0o12),(2,0xa,1_000)", 2))
					check(referenceSelect("SELECT id,i,b FROM t ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}}))
					referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(referenceExec("INSERT INTO t VALUES ($1,0B_1_010,0X_A)", 1, int64(3))))
					check(referenceSelect("SELECT id,i,b FROM t WHERE i=0b1010 ORDER BY id", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(10), int64(10)}, {int64(2), int64(10), int64(1000)}, {int64(3), int64(10), int64(10)}}))
					check(referenceSelect("SELECT id,i,b FROM t WHERE b=1_000", []string{"id", "i", "b"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(2), int64(10), int64(1000)}}))
					run(referenceExec("CREATE TABLE endpoints(id INT,b BIGINT)", 0))
					var want [][]any
					for i, literal := range numericEndpoints() {
						run(referenceExec(fmt.Sprintf("INSERT INTO endpoints VALUES (%d,%s)", i, literal), 1))
						value := int64(9223372036854775807)
						if i%2 == 1 {
							value = -9223372036854775808
						}
						want = append(want, []any{int64(i), value})
					}
					check(referenceSelect("SELECT id,b FROM endpoints ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, want))
					run(referenceExec("CREATE TABLE bounds(i INT,b BIGINT,label TEXT)", 0))
					run(referenceExec("INSERT INTO bounds VALUES (-0x8000_0000,0x8000_0000,0xa),(0x7FFF_FFFF,010,1_000)", 2))
					check(referenceSelect("SELECT i,b,label FROM bounds ORDER BY i", []string{"i", "b", "label"}, []string{"INT4", "INT8", "TEXT"}, [][]any{{int64(-2147483648), int64(2147483648), "10"}, {int64(2147483647), int64(10), "1000"}}))
				})
			}
		})
	}
}

func TestReferenceNumericLiteralsSpellings(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			p := newReferenceScenario(t)
			p.step(t, referenceExec("CREATE TABLE t(id INT,b BIGINT)", 0))
			var want [][]any
			for i, tc := range numericLiteralSpellings() {
				s := referenceExec(fmt.Sprintf("INSERT INTO t VALUES (%d,%s)", i, tc.literal), 1)
				s.prepared = prepared
				p.step(t, s)
				want = append(want, []any{int64(i), tc.value})
			}
			s := referenceSelect("SELECT id,b FROM t ORDER BY id", []string{"id", "b"}, []string{"INT4", "INT8"}, want)
			p.step(t, s)
			for _, backend := range p.backends() {
				rows, err := backend.db.QueryContext(p.ctx, s.query)
				if err != nil {
					t.Fatal(err)
				}
				lexicalRows(t, backend.pg, rows, s, nil)
			}
		})
	}
}
