package reference_test

import "testing"

func TestReferenceQueryAggregateTypes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, tc := range []struct {
		name, typ, values string
		counts            [][]any
	}{
		{"bools", "BOOL", "(TRUE),(TRUE),(FALSE),(NULL),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(2)}}},
		{"smallints", "SMALLINT", "(1),(1),(2),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(1)}}},
		{"ints", "INT", "(1),(1),(2),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(1)}}},
		{"bigints", "BIGINT", "(9223372036854775807),(9223372036854775807),(0),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(1)}}},
		{"texts", "TEXT", "('a'),('a'),('a '),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(1)}}},
		{"varchars", "VARCHAR(4)", "('a'),('a '),('a'),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(1)}}},
		{"chars", "CHAR(4)", "('a'),('a '),('b'),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(1)}}},
		{"numerics", "NUMERIC", "(1),(1.00),(2),('NaN'),('NaN'),('Infinity'),('-Infinity'),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(2)}, {int64(1)}, {int64(1)}, {int64(1)}}},
		{"reals", "REAL", "('-0'),('0'),('NaN'),('NaN'),('Infinity'),('-Infinity'),(NULL)", [][]any{{int64(2)}, {int64(2)}, {int64(1)}, {int64(1)}, {int64(1)}}},
		{"doubles", "DOUBLE PRECISION", "('-0'),('0'),('NaN'),('NaN'),('Infinity'),('-Infinity'),(NULL)", [][]any{{int64(2)}, {int64(2)}, {int64(1)}, {int64(1)}, {int64(1)}}},
		{"bytes", "BYTEA", `('\x00ff'),('\x00ff'),('\x0000'),(NULL)`, [][]any{{int64(2)}, {int64(1)}, {int64(1)}}},
		{"dates", "DATE", "('infinity'),('infinity'),('-infinity'),('2000-01-01'),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(1)}, {int64(1)}}},
		{"times", "TIME", "('01:00'),('01:00'),('02:00'),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(1)}}},
		{"timestamps", "TIMESTAMP", "('infinity'),('infinity'),('-infinity'),('2000-01-01'),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(1)}, {int64(1)}}},
		{"uuids", "UUID", "('00000000-0000-0000-0000-000000000001'),('00000000-0000-0000-0000-000000000001'),('00000000-0000-0000-0000-000000000002'),(NULL)", [][]any{{int64(2)}, {int64(1)}, {int64(1)}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p.step(t, referenceExec("CREATE TABLE "+tc.name+"(v "+tc.typ+")", 0))
			var count int64
			for _, row := range tc.counts {
				count += row[0].(int64)
			}
			p.step(t, referenceExec("INSERT INTO "+tc.name+" VALUES"+tc.values, count))
			step := referenceSelect("SELECT count(*) FROM "+tc.name+" GROUP BY v", []string{"count"}, []string{"INT8"}, tc.counts)
			step.bag = true
			p.step(t, step)
			p.step(t, referencePrepared(step))
		})
	}
	p.step(t, referenceExec("CREATE TABLE oid_rows(v INT)", 0))
	p.step(t, referenceExec("INSERT INTO oid_rows VALUES(1),(2)", 2))
	p.step(t, referenceExec("CREATE SEQUENCE group_sequence", 0))
	for _, q := range []string{"SELECT count(*) FROM oid_rows GROUP BY CAST('group_sequence' AS regclass)", "SELECT count(*) FROM oid_rows GROUP BY CAST('4294967295' AS regclass)", "SELECT count(*) FROM oid_rows GROUP BY CAST('0' AS regclass)"} {
		step := referenceSelect(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}})
		p.step(t, step)
		p.step(t, referencePrepared(step))
	}
}
