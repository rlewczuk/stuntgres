//go:build reference

package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryOrderRegclass(t *testing.T) {
	p := fixture.NewPair(t)
	p.Step(t, fixture.Exec("CREATE TABLE t(id INT)", 0))
	query := "SELECT id FROM t ORDER BY CASE id WHEN 1 THEN '4294967295'::regclass WHEN 2 THEN '1'::regclass ELSE '2147483648'::regclass END"
	step := fixture.Select(query, []string{"id"}, []string{"INT4"}, nil)
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	p.Step(t, fixture.Exec("INSERT INTO t VALUES(1),(2),(3)", 3))
	step.Rows = [][]any{{int64(2)}, {int64(3)}, {int64(1)}}
	p.Step(t, step)
	p.Step(t, fixture.Prepared(step))
	p.Step(t, fixture.Select("SELECT NULL::regclass AS r FROM t ORDER BY r,id", []string{"r"}, []string{"REGCLASS"}, [][]any{{nil}, {nil}, {nil}}))
}
