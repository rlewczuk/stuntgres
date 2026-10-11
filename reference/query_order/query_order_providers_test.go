//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderProviders(t *testing.T) {
	for _, tc := range []struct {
		name, typ, values string
		ids               []int64
	}{
		{"bool", "BOOL", "(1,TRUE),(2,FALSE),(3,NULL)", []int64{2, 1, 3}},
		{"oid", "OID", "(1,'4294967295'),(2,'1'),(3,'2147483648'),(4,NULL)", []int64{2, 3, 1, 4}},
		{"numeric", "NUMERIC", "(1,'NaN'),(2,'Infinity'),(3,'-Infinity'),(4,'-1'),(5,'0'),(6,NULL)", []int64{3, 4, 5, 2, 1, 6}},
		{"float", "DOUBLE PRECISION", "(1,'NaN'),(2,'Infinity'),(3,'-Infinity'),(4,'-1'),(5,'0'),(6,NULL)", []int64{3, 4, 5, 2, 1, 6}},
		{"uuid", "UUID", "(1,'ffffffff-ffff-ffff-ffff-ffffffffffff'),(2,'00000000-0000-0000-0000-000000000001'),(3,NULL)", []int64{2, 1, 3}},
		{"bytea", "BYTEA", `(1,'\xff'),(2,'\x00'),(3,'\x01'),(4,'\x0001'),(5,NULL)`, []int64{2, 4, 3, 1, 5}},
		{"text", "TEXT", "(1,'é'),(2,'a'),(3,'Z'),(4,'A'),(5,NULL)", []int64{4, 3, 2, 1, 5}},
		{"bpchar", "CHAR(3)", "(1,'a '),(2,'b'),(3,'a'),(4,NULL)", []int64{1, 3, 2, 4}},
		{"date", "DATE", "(1,'infinity'),(2,'2000-01-01'),(3,'-infinity'),(4,NULL)", []int64{3, 2, 1, 4}},
		{"timestamp", "TIMESTAMP", "(1,'infinity'),(2,'2000-01-01'),(3,'-infinity'),(4,NULL)", []int64{3, 2, 1, 4}},
		{"time", "TIME", "(1,'24:00:00'),(2,'00:00:00'),(3,'12:00:00'),(4,NULL)", []int64{2, 3, 1, 4}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewPair(t)
			p.Step(t, fixture.Exec("CREATE TABLE t(id INT,v "+tc.typ+")", 0))
			// Provider resolution is analysis-time, independent of cardinality.
			step := fixture.Select("SELECT id FROM t ORDER BY v ASC NULLS LAST,id", []string{"id"}, []string{"INT4"}, nil)
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES"+tc.values, int64(len(tc.ids))))
			for _, id := range tc.ids {
				step.Rows = append(step.Rows, []any{id})
			}
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
			p.Step(t, fixture.Exec("DELETE FROM t", int64(len(tc.ids))))
			p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,NULL)", 1))
			step.Rows = [][]any{{int64(1)}}
			p.Step(t, step)
			p.Step(t, fixture.Prepared(step))
		})
	}
}

func TestReferenceQueryOrderValuesAndAtomicity(t *testing.T) {
	p := fixture.NewPair(t)
	p.Step(t, fixture.Exec("CREATE TABLE d(id INT,a INT)", 0))
	for _, key := range []string{"column2 DESC NULLS LAST,column1", "column2+0 DESC NULLS LAST,column1", "2 DESC NULLS LAST,1"} {
		step := fixture.Exec("INSERT INTO d VALUES(1,2),(2,1),(3,NULL),(4,2) ORDER BY "+key, 4)
		p.Step(t, step)
		p.Step(t, fixture.Prepared(step))
		p.Step(t, fixture.Select("SELECT id,a FROM d ORDER BY id,a", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(1), int64(2)}, {int64(2), int64(1)}, {int64(2), int64(1)}, {int64(3), nil}, {int64(3), nil}, {int64(4), int64(2)}, {int64(4), int64(2)}}))
		p.Step(t, fixture.Exec("DELETE FROM d", 8))
	}
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT,a INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1,2),(2,1),(3,NULL),(4,2)", 4))
	p.Step(t, fixture.Exec("INSERT INTO d VALUES(9,9)", 1))
	for _, prepared := range []bool{false, true} {
		step := fixture.ImplicitError("INSERT INTO d SELECT id,a FROM t ORDER BY a,1/(id-4)", fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		step.Prepared = prepared
		step.ErrorStage = "execute"
		p.Step(t, step)
		p.Step(t, fixture.Select("SELECT id,a FROM d ORDER BY id", []string{"id", "a"}, []string{"INT4", "INT4"}, [][]any{{int64(9), int64(9)}}))
	}
}
