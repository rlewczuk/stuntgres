package reference_test

import (
	"bytes"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func expressionHierarchyRows(pg bool, rows ...[]any) fixture.Step {
	q := "SELECT s.stxname,d.stxdinherit,(stxdexpr[1]).stanullfrac::text AS null_fraction,(stxdexpr[1]).stadistinct,(stxdexpr[1]).stakind1,(stxdexpr[1]).stavalues1::text AS values,(stxdexpr[1]).stanumbers1::text AS frequencies,d.stxdndistinct,array_length(stxdexpr,1) AS expressions FROM pg_statistic_ext s JOIN pg_statistic_ext_data d ON d.stxoid=s.oid WHERE s.stxname IN ('expr_h_parent_s','expr_h_child_s','expr_h_part_s') ORDER BY s.stxname,d.stxdinherit"
	return ndistinctRawResult(pg, fixture.Select(q, []string{"stxname", "stxdinherit", "null_fraction", "stadistinct", "stakind1", "values", "frequencies", "stxdndistinct", "expressions"}, []string{"NAME", "BOOL", "TEXT", "FLOAT4", "INT2", "TEXT", "TEXT", statisticsDatabaseTypeName(pg, "pg_ndistinct"), "INT4"}, rows), 7)
}

func expressionHierarchyOld(pg bool, partition bool) fixture.Step {
	rows := [][]any{
		{"expr_h_child_s", false, "0.2", float64(2), int64(1), "{4,5}", "{0.5,0.3}", `{"3, -1": 3}`, int64(1)},
		{"expr_h_parent_s", false, "0.2", float64(2), int64(1), "{2,3}", "{0.7,0.1}", `{"2, -1": 3}`, int64(1)},
		{"expr_h_parent_s", true, "0.2", float64(4), int64(1), "{2,4,5,3}", "{0.35,0.25,0.15,0.05}", `{"2, -1": 5}`, int64(1)},
	}
	if partition {
		rows = append(rows, []any{"expr_h_part_s", true, "0", float64(2), int64(1), "{2,12}", "{0.7,0.3}", `{"2, -1": 2}`, int64(1)})
	}
	return expressionHierarchyRows(pg, rows...)
}

func expressionHierarchyFresh(pg bool) fixture.Step {
	return expressionHierarchyRows(pg,
		[]any{"expr_h_child_s", false, "0", float64(1), int64(1), "{10}", "{1}", `{"3, -1": 1}`, int64(1)},
		[]any{"expr_h_parent_s", false, "0", float64(1), int64(1), "{10}", "{1}", `{"2, -1": 1}`, int64(1)},
		[]any{"expr_h_parent_s", true, "0", float64(1), int64(1), "{10}", "{1}", `{"2, -1": 1}`, int64(1)},
		[]any{"expr_h_part_s", true, "0", float64(2), int64(1), "{2,12}", "{0.7,0.3}", `{"2, -1": 2}`, int64(1)},
	)
}

func TestReferenceStatisticsExpressionHierarchyControls(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		for _, q := range []string{
			"CREATE TABLE expr_h_parent(a INT,b INT)",
			"CREATE TABLE expr_h_child(extra INT,a INT,b INT)",
			"ALTER TABLE expr_h_child INHERIT expr_h_parent",
			"CREATE STATISTICS expr_h_parent_s(ndistinct) ON (a+1),b FROM expr_h_parent",
			"CREATE STATISTICS expr_h_child_s(ndistinct) ON (a+1),b FROM expr_h_child",
		} {
			s.run(fixture.Exec(q, 0))
		}
		// Unlike CREATE ... INHERITS, attaching this existing child puts a/b
		// in slots 2/3, not the parent's 1/2. Reading slot 1 yields 778, not
		// any independently expected parent expression result below.
		s.run(fixture.Select("SELECT attnum,attname FROM pg_attribute WHERE attrelid='expr_h_child'::regclass AND attnum>0 ORDER BY attnum", []string{"attnum", "attname"}, []string{"INT2", "NAME"}, [][]any{{int64(1), "extra"}, {int64(2), "a"}, {int64(3), "b"}}))
		parent := strings.TrimSuffix(strings.Repeat("(1,10),", 14)+strings.Repeat("(2,20),", 2)+strings.Repeat("(NULL,NULL),", 4), ",")
		child := strings.TrimSuffix(strings.Repeat("(777,3,30),", 10)+strings.Repeat("(777,4,40),", 6)+strings.Repeat("(777,NULL,NULL),", 4), ",")
		s.run(fixture.Exec("INSERT INTO expr_h_parent VALUES"+parent, 20))
		s.run(fixture.Exec("INSERT INTO expr_h_child VALUES"+child, 20))
		s.run(expressionHierarchyRows(s.pg))
		s.run(fixture.Exec("ANALYZE expr_h_parent", 0))
		s.run(expressionHierarchyOld(s.pg, false))
		s.run(expressionDefinition("expr_h_parent_s", "2", "{d,e}"))
		s.run(expressionDefinition("expr_h_child_s", "3", "{d,e}"))

		for _, q := range []string{
			"CREATE TABLE expr_h_part(a INT,b INT) PARTITION BY RANGE(a)",
			"CREATE TABLE expr_h_low PARTITION OF expr_h_part FOR VALUES FROM(0) TO(10)",
			"CREATE TABLE expr_h_high PARTITION OF expr_h_part FOR VALUES FROM(10) TO(20)",
			"CREATE STATISTICS expr_h_part_s(ndistinct) ON (a+1),b FROM expr_h_part",
		} {
			s.run(fixture.Exec(q, 0))
		}
		partition := strings.TrimSuffix(strings.Repeat("(1,10),", 14)+strings.Repeat("(11,110),", 6), ",")
		s.run(fixture.Exec("INSERT INTO expr_h_part VALUES"+partition, 20))
		s.run(fixture.Exec("ANALYZE expr_h_part", 0))
		// Exact rows require inherited-only partition data: there must not be
		// a fabricated local population for the partitioned parent.
		s.run(expressionHierarchyOld(s.pg, true))
		s.run(fixture.Exec("UPDATE expr_h_parent SET a=9,b=90", 40))
		s.run(expressionHierarchyOld(s.pg, true))
		if !s.pg {
			var saved bytes.Buffer
			if err := stuntgres.Dump(s.pair.Ctx, s.pair.Native, &saved); err != nil {
				t.Fatal(err)
			}
			if saved.Len() == 0 {
				t.Fatal("empty inherited expression dump")
			}
			if err := stuntgres.Reset(s.pair.Ctx, s.pair.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(s.pair.Ctx, s.pair.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		s.run(expressionHierarchyOld(s.pg, true))
		s.run(fixture.Select("SELECT min(a),max(a),count(*) FROM expr_h_parent", []string{"min", "max", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(9), int64(9), int64(40)}}))
		s.run(fixture.Select("SELECT min(extra),max(extra),count(*) FROM expr_h_child", []string{"min", "max", "count"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(777), int64(777), int64(20)}}))
		s.run(fixture.Exec("ANALYZE expr_h_parent", 0))
		s.run(expressionHierarchyFresh(s.pg))
	})
}
