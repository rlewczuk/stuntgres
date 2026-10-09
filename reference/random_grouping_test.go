package reference_test

import (
	"strings"
	"testing"
)

func TestReferenceRandomGroupingAndTyping(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE random_source(id INT,lo INT,hi INT)", 0))
	p.step(t, referenceExec("INSERT INTO random_source VALUES(1,7,7),(2,7,7),(3,9,9)", 3))
	for _, s := range []referenceStep{
		referenceSelect("SELECT random(lo,hi) FROM random_source ORDER BY id", []string{"random"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(9)}}),
		referenceSelect("SELECT random(count(*),count(*)) FROM random_source", []string{"random"}, []string{"INT8"}, [][]any{{int64(3)}}),
		referenceSelect("SELECT random(lo,hi),count(*) FROM random_source GROUP BY 1", []string{"random", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(2)}, {int64(9), int64(1)}}),
		referenceSelect("SELECT random(7,$1)", []string{"random"}, []string{"INT4"}, [][]any{{int64(7)}}, 7),
		referenceSelect("SELECT random(CASE WHEN TRUE THEN 7 ELSE 1/0 END,7) FROM random_source WHERE id=1", []string{"random"}, []string{"INT4"}, [][]any{{int64(7)}}),
	} {
		s.bag = strings.Contains(s.query, "GROUP BY")
		p.step(t, s)
		p.step(t, referencePrepared(s))
	}
}
