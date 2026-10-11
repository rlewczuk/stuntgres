package reference_test

import (
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRandomGroupingAndTyping(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE random_source(id INT,lo INT,hi INT)", 0))
	p.Step(t, fixture.Exec("INSERT INTO random_source VALUES(1,7,7),(2,7,7),(3,9,9)", 3))
	for _, s := range []fixture.Step{
		fixture.Select("SELECT random(lo,hi) FROM random_source ORDER BY id", []string{"random"}, []string{"INT4"}, [][]any{{int64(7)}, {int64(7)}, {int64(9)}}),
		fixture.Select("SELECT random(count(*),count(*)) FROM random_source", []string{"random"}, []string{"INT8"}, [][]any{{int64(3)}}),
		fixture.Select("SELECT random(lo,hi),count(*) FROM random_source GROUP BY 1", []string{"random", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(7), int64(2)}, {int64(9), int64(1)}}),
		fixture.Select("SELECT random(7,$1)", []string{"random"}, []string{"INT4"}, [][]any{{int64(7)}}, 7),
		fixture.Select("SELECT random(CASE WHEN TRUE THEN 7 ELSE 1/0 END,7) FROM random_source WHERE id=1", []string{"random"}, []string{"INT4"}, [][]any{{int64(7)}}),
	} {
		s.Bag = strings.Contains(s.Query, "GROUP BY")
		p.Step(t, s)
		p.Step(t, fixture.Prepared(s))
	}
}
