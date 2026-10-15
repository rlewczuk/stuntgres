package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

var moreFamilies = []struct {
	rangeName, multiName, rangeType, multiType, finite, canonical, members, first, second string
}{
	{"int4range", "int4multirange", "INT4RANGE", "INT4MULTIRANGE", "[1,4]", "[1,5)", "{[8,9),empty,[1,3),[2,4),[9,10)}", "[1,4)", "[8,10)"},
	{"int8range", "int8multirange", "INT8RANGE", "INT8MULTIRANGE", "[1,4]", "[1,5)", "{[8,9),empty,[1,3),[2,4),[9,10)}", "[1,4)", "[8,10)"},
	{"numrange", "nummultirange", "NUMRANGE", "NUMMULTIRANGE", "(1.00,2.000]", "(1.00,2.000]", "{[8,9),empty,(1.00,2.000],(2.000,3.0]}", "(1.00,3.0]", "[8,9)"},
}

func moreScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func moreBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
}

func TestReferenceRangeMoreValues(t *testing.T) {
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, f := range moreFamilies {
			t.Run(f.rangeName, func(t *testing.T) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE more_values(k integer,a "+f.rangeName+",m "+f.multiName+")", 0))
				run(fixture.Exec("INSERT INTO more_values VALUES (1,"+rangeLiteral(f.finite)+","+rangeLiteral(f.members)+"),(2,'empty','{}'),(3,NULL,NULL),(4,'(,)','{(,)}'),(5,'empty','{empty}')", 5))
				moreBoth(t, p, b, rangeSelect(b, "SELECT multirange(a) AS m FROM more_values ORDER BY k", []string{"m"}, []string{f.multiType}, [][]any{{"{" + f.canonical + "}"}, {"{}"}, {nil}, {"{(,)}"}, {"{}"}}, nil))
				// No ORDER BY: this observes the selected ascending-member guarantee.
				moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(m) AS r FROM more_values WHERE k=1", []string{"r"}, []string{f.rangeType}, [][]any{{f.first}, {f.second}}, nil))
				for _, k := range []string{"2", "3", "5"} {
					moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(m) AS r FROM more_values WHERE k="+k, []string{"r"}, []string{f.rangeType}, [][]any{}, nil))
					moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(multirange(a)) AS r FROM more_values WHERE k="+k, []string{"r"}, []string{f.rangeType}, [][]any{}, nil))
				}
				moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(m) AS r FROM more_values WHERE k=4", []string{"r"}, []string{f.rangeType}, [][]any{{"(,)"}}, nil))
				moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(multirange(a)) AS r FROM more_values WHERE k=1", []string{"r"}, []string{f.rangeType}, [][]any{{f.canonical}}, nil))
				run(fixture.Exec("DROP TABLE more_values", 0))
			})
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE more_gap(m nummultirange)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO more_gap VALUES ('{[1,2),(2,3]}')", 1))
		moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(m) AS r FROM more_gap", []string{"r"}, []string{"NUMRANGE"}, [][]any{{"[1,2)"}, {"(2,3]"}}, nil))
	})
}

func TestReferenceRangeMoreMetadata(t *testing.T) {
	moreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, f := range moreFamilies {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN more_base AS "+f.rangeName, 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN more_outer AS more_base", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN more_multi AS "+f.multiName, 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN more_multi_outer AS more_multi", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE more_metadata(a more_outer,m more_multi_outer)", 0))
			for _, q := range []string{"SELECT multirange(a) AS m FROM more_metadata", "SELECT multirange(a) AS m FROM more_metadata LIMIT 0"} {
				moreBoth(t, p, b, rangeSelect(b, q, []string{"m"}, []string{f.multiType}, [][]any{}, nil))
			}
			moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(m) AS r FROM more_metadata", []string{"r"}, []string{f.rangeType}, [][]any{}, nil))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO more_metadata VALUES (NULL,NULL),(NULL,NULL)", 2))
			moreBoth(t, p, b, rangeSelect(b, "SELECT multirange(a) AS m FROM more_metadata", []string{"m"}, []string{f.multiType}, [][]any{{nil}, {nil}}, nil))
			moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(m) AS r FROM more_metadata", []string{"r"}, []string{f.rangeType}, [][]any{}, nil))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO more_metadata VALUES ("+rangeLiteral(f.finite)+","+rangeLiteral(f.members)+")", 1))
			moreBoth(t, p, b, rangeSelect(b, "SELECT multirange(a) AS m FROM more_metadata WHERE a IS NOT NULL", []string{"m"}, []string{f.multiType}, [][]any{{"{" + f.canonical + "}"}}, nil))
			moreBoth(t, p, b, rangeSelect(b, "SELECT unnest(m) AS r FROM more_metadata", []string{"r"}, []string{f.rangeType}, [][]any{{f.first}, {f.second}}, nil))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE more_metadata", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP DOMAIN more_outer", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP DOMAIN more_base", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP DOMAIN more_multi_outer", 0))
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP DOMAIN more_multi", 0))
		}
	})
}
