package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const expressionDataFrom = " FROM pg_statistic_ext s JOIN pg_statistic_ext_data d ON d.stxoid=s.oid WHERE s.stxname=$1 AND NOT d.stxdinherit"

func expressionDefinition(name, keys, kinds string) fixture.Step {
	return fixture.Select("SELECT stxkeys,stxkind,stxexprs IS NOT NULL AS expression_tree FROM pg_statistic_ext WHERE stxname=$1", []string{"stxkeys", "stxkind", "expression_tree"}, []string{"INT2VECTOR", "_CHAR", "BOOL"}, [][]any{{keys, kinds, true}}, name)
}

func expressionPopulation(s ndistinctScenario) {
	s.run(fixture.Exec("CREATE TABLE expr_t(a INT,b INT,spare INT)", 0))
	values := make([]string, 0, 20)
	for range 14 {
		values = append(values, "(1,10,0)")
	}
	for range 2 {
		values = append(values, "(2,20,0)")
	}
	for range 4 {
		values = append(values, "(NULL,NULL,0)")
	}
	s.run(fixture.Exec("INSERT INTO expr_t VALUES"+strings.Join(values, ","), 20))
}

// The public FLOAT4 comparisons allow transport's binary32-to-float64 rounding,
// not collector inaccuracies. Frequencies are still asserted independently.
func expressionSkew(name string, index int, values string) fixture.Step {
	row := fmt.Sprintf("(stxdexpr[%d])", index)
	q := "SELECT abs(" + row + ".stanullfrac-0.2)<0.000001 AS null_fraction," + row + ".stadistinct," + row + ".stakind1," + row + ".stavalues1::text AS values," + row + ".stanumbers1::text AS frequencies,NOT (2 IN (" + expressionSlotKinds(row) + ")) AS no_histogram" + expressionDataFrom
	return fixture.Select(q, []string{"null_fraction", "stadistinct", "stakind1", "values", "frequencies", "no_histogram"}, []string{"BOOL", "FLOAT4", "INT2", "TEXT", "TEXT", "BOOL"}, [][]any{{true, float64(2), int64(1), values, "{0.7,0.1}", true}}, name)
}

func expressionSlotKinds(row string) string {
	kinds := make([]string, 5)
	for i := range kinds {
		kinds[i] = fmt.Sprintf("%s.stakind%d", row, i+1)
	}
	return strings.Join(kinds, ",")
}

func expressionJoint(pg bool, name, value string, expressions int) fixture.Step {
	q := "SELECT stxdndistinct,array_length(stxdexpr,1) AS expressions" + expressionDataFrom
	return ndistinctRawResult(pg, fixture.Select(q, []string{"stxdndistinct", "expressions"}, []string{statisticsDatabaseTypeName(pg, "pg_ndistinct"), "INT4"}, [][]any{{value, int64(expressions)}}, name), 0)
}

func expressionMissingData() fixture.Step {
	return fixture.Select("SELECT count(*) FROM pg_statistic_ext_data d JOIN pg_statistic_ext s ON s.oid=d.stxoid WHERE s.stxrelid='expr_t'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}})
}

func expressionRawDatum(t *testing.T, s ndistinctScenario, name string) {
	t.Helper()
	rows, err := s.conn.QueryContext(s.pair.Ctx, "SELECT stxdexpr"+expressionDataFrom, name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(columns) != 1 || columns[0].DatabaseTypeName() != statisticsDatabaseTypeName(s.pg, "_pg_statistic") {
		t.Fatal("expression array metadata", columns)
	}
	if !rows.Next() {
		t.Fatal("missing real pg_statistic[]", rows.Err())
	}
	var value any
	if err := rows.Scan(&value); err != nil {
		t.Fatal(err)
	}
	if s.pg {
		if _, ok := value.([]byte); !ok {
			t.Fatalf("PostgreSQL array carrier %T", value)
		}
	} else {
		if _, ok := value.(string); !ok {
			t.Fatalf("native array carrier %T", value)
		}
	}
	text := fixture.Normalize(value).(string)
	if !strings.HasPrefix(text, `{"(`) || !strings.HasSuffix(text, `)"}`) || !strings.Contains(text, "0.2") {
		t.Fatalf("not a genuine populated statistic row array: %q", text)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected array rows", rows.Err())
	}
}
