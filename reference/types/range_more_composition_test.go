package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeMoreComposition(t *testing.T) {
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE more_comp(k integer,a int4range,m int4multirange,n int8multirange)", 0))
		run(fixture.Exec("INSERT INTO more_comp VALUES (1,'[1,4]','{[8,9),[1,3),[2,4),[9,10)}','{[20,21)}'),(2,'empty','{}','{}'),(3,NULL,NULL,NULL)", 3))
		for _, q := range []string{
			"SELECT r FROM more_comp,unnest(m) AS u(r) WHERE k=1",
			"SELECT r FROM more_comp CROSS JOIN LATERAL unnest(m) AS u(r) WHERE k=1",
			"SELECT r FROM unnest((SELECT m FROM more_comp WHERE k=1)) AS u(r)",
		} {
			moreBoth(t, p, b, rangeSelect(b, q, []string{"r"}, []string{"INT4RANGE"}, [][]any{{"[1,4)"}, {"[8,10)"}}, nil))
		}
		moreBoth(t, p, b, rangeSelect(b, "SELECT r,ord FROM more_comp,unnest(m) WITH ORDINALITY AS u(r,ord) WHERE k=1", []string{"r", "ord"}, []string{"INT4RANGE", "INT8"}, [][]any{{"[1,4)", int64(1)}, {"[8,10)", int64(2)}}, nil))
		for _, q := range []string{
			"SELECT unnest(m) AS r,unnest(n) AS s FROM more_comp WHERE k=1",
			"SELECT r,s FROM more_comp,ROWS FROM(unnest(m),unnest(n)) AS u(r,s) WHERE k=1",
		} {
			moreBoth(t, p, b, rangeSelect(b, q, []string{"r", "s"}, []string{"INT4RANGE", "INT8RANGE"}, [][]any{{"[1,4)", "[20,21)"}, {"[8,10)", nil}}, nil))
		}
		moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(m) AS r,unnest(n) AS s FROM more_comp WHERE k=2", []string{"r", "s"}, []string{"INT4RANGE", "INT8RANGE"}, [][]any{}, nil))
		moreBoth(t, p, b, rangeSelect(b, "SELECT multirange(unnest(m)) AS m FROM more_comp WHERE k=1", []string{"m"}, []string{"INT4MULTIRANGE"}, [][]any{{"{[1,4)}"}, {"{[8,10)}"}}, nil))
		moreBoth(t, p, b, fixture.Select("SELECT lower(unnest(m)) AS lo FROM more_comp WHERE k=1", []string{"lo"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(8)}}))
		moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(pg_catalog.multirange(a)) AS r FROM more_comp WHERE k=1", []string{"r"}, []string{"INT4RANGE"}, [][]any{{"[1,5)"}}, nil))
		moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(m) AS r FROM more_comp LIMIT 0", []string{"r"}, []string{"INT4RANGE"}, [][]any{}, nil))
		moreBoth(t, p, b, rangeSelect(b, "SELECT multirange(a) AS m FROM more_comp WHERE false", []string{"m"}, []string{"INT4MULTIRANGE"}, [][]any{}, nil))
		moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(m) AS r,generate_series(1,3) AS i FROM more_comp WHERE k=1", []string{"r", "i"}, []string{"INT4RANGE", "INT4"}, [][]any{{"[1,4)", int64(1)}, {"[8,10)", int64(2)}, {nil, int64(3)}}, nil))
		run(fixture.Exec("INSERT INTO more_comp SELECT 4,a,m,n FROM more_comp WHERE k=1", 1))
		s := rangeSelect(b, "SELECT unnest(m) AS r FROM more_comp WHERE k=1 OR k=4", []string{"r"}, []string{"INT4RANGE"}, [][]any{{"[1,4)"}, {"[8,10)"}, {"[1,4)"}, {"[8,10)"}}, nil)
		s.Bag = true
		moreBoth(t, p, b, s)
		moreBoth(t, p, b, rangeSelect(b, "SELECT k,r,ord FROM more_comp,unnest(m) WITH ORDINALITY AS u(r,ord) ORDER BY k,ord", []string{"k", "r", "ord"}, []string{"INT4", "INT4RANGE", "INT8"}, [][]any{{int64(1), "[1,4)", int64(1)}, {int64(1), "[8,10)", int64(2)}, {int64(4), "[1,4)", int64(1)}, {int64(4), "[8,10)", int64(2)}}, nil))
	})
}
