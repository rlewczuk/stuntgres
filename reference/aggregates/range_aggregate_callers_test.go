package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func rangeAggregateCallers(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(k INT,v INT4RANGE)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES(1,'[1,4)'),(1,'[3,7)'),(2,'[8,9)'),(3,NULL)", 4))
	families := []string{"INT4MULTIRANGE", "INT4RANGE"}
	for _, q := range []string{
		"SELECT range_agg(v),range_intersect_agg(v) FROM r WHERE k=1",
		"SELECT range_agg(v) FILTER(WHERE k=1),range_intersect_agg(v) FILTER(WHERE k=1) FROM r",
		"SELECT range_agg(v),range_intersect_agg(v) FROM (SELECT v FROM r WHERE k=1) AS z",
	} {
		rangeAggregateRoutes(t, p, b, rangeAggregateStep(b.PG, q, []string{"range_agg", "range_intersect_agg"}, families, [][]any{{"{[1,7)}", "[3,4)"}}))
	}
	s := rangeAggregateStep(b.PG, "SELECT k,range_agg(v),range_intersect_agg(v) FROM r GROUP BY k", []string{"k", "range_agg", "range_intersect_agg"}, []string{"INT4", "INT4MULTIRANGE", "INT4RANGE"}, [][]any{{int64(1), "{[1,7)}", "[3,4)"}, {int64(2), "{[8,9)}", "[8,9)"}, {int64(3), nil, nil}})
	s.Bag = true
	rangeAggregateRoutes(t, p, b, s)
	rangeAggregateRoutes(t, p, b, rangeAggregateStep(b.PG, "SELECT k FROM r GROUP BY k HAVING range_agg(v) IS NULL AND range_intersect_agg(v) IS NULL", []string{"k"}, []string{"INT4"}, [][]any{{int64(3)}}))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW ra_view AS SELECT k,range_agg(v) AS u,range_intersect_agg(v) AS i FROM r GROUP BY k", 0))
	s.Query = "SELECT k,u,i FROM ra_view"
	s.Columns = []string{"k", "u", "i"}
	rangeAggregateRoutes(t, p, b, s)
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE dest(k INT,u INT4MULTIRANGE,i INT4RANGE)", 0))
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			q string
			n int64
		}{
			{"INSERT INTO dest(u,i) SELECT range_agg(v),range_intersect_agg(v) FROM r WHERE FALSE", 1},
			{"INSERT INTO dest SELECT k,range_agg(v),range_intersect_agg(v) FROM r WHERE FALSE GROUP BY k", 0},
			{"INSERT INTO dest SELECT k,range_agg(v),range_intersect_agg(v) FROM r GROUP BY k", 3},
		} {
			step := fixture.Exec(tc.q, tc.n)
			step.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, step)
		}
	}
	rangeAggregateRoutes(t, p, b, fixture.Select("SELECT count(*) FROM dest", []string{"count"}, []string{"INT8"}, [][]any{{int64(8)}}))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE required(k INT,u INT4MULTIRANGE NOT NULL)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO required VALUES(9,'{[90,91)}')", 1))
	d := fixture.ControlsReferenceDiagnostic("23502", `null value in column "u" of relation "required" violates not-null constraint`)
	d.Detail, d.Schema, d.Table, d.Column = "Failing row contains (3, null).", "public", "required", "u"
	for _, prepared := range []bool{false, true} {
		s := fixture.ErrorDiagnostic("INSERT INTO required SELECT k,range_agg(v) FROM r GROUP BY k ORDER BY k", d)
		s.Prepared, s.ErrorStage = prepared, "execute"
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
	rangeAggregateRoutes(t, p, b, rangeAggregateStep(b.PG, "SELECT k,u FROM required", []string{"k", "u"}, []string{"INT4", "INT4MULTIRANGE"}, [][]any{{int64(9), "{[90,91)}"}}))
	q := "SELECT range_agg(v),range_intersect_agg(v) FROM r WHERE k=1"
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES(1,'[6,9)')", 1))
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, q}, b.Recorder, rangeAggregateStep(b.PG, q, []string{"range_agg", "range_intersect_agg"}, families, [][]any{{"{[1,9)}", "empty"}}))
	// Successful range binds use existing destination-context input, not the
	// independently excluded explicit range-cast surface.
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE ra_binds(v INT4RANGE)", 0))
	boundRows := int64(0)
	for _, input := range []any{"[2,5)", []byte("[2,5)"), nil} {
		var union, intersection any = "{[2,5)}", "[2,5)"
		if input == nil {
			union, intersection = nil, nil
		}
		for _, prepared := range []bool{false, true} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM ra_binds", boundRows))
			insert := fixture.Exec("INSERT INTO ra_binds(v) VALUES($1)", 1, input)
			insert.Prepared = prepared
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, insert)
			boundRows = 1
			rangeAggregateRoutes(t, p, b, rangeAggregateStep(b.PG, "SELECT range_agg(v),range_intersect_agg(v) FROM ra_binds", []string{"range_agg", "range_intersect_agg"}, families, [][]any{{union, intersection}}))
		}
	}
}

func TestReferenceRangeAggregateCallers(t *testing.T) {
	rangeAggregateScenario(t, rangeAggregateCallers)
}

func TestReferenceRangeAggregateFeatureSmoke(t *testing.T) {
	rangeAggregateScenario(t, rangeAggregateCallers)
}
