package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantRecentRegressionAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, typ string
		value     any
	}{
		{"corr", "FLOAT8", float64(1)},
		{"covar_pop", "FLOAT8", float64(2)},
		{"covar_samp", "FLOAT8", float64(4)},
		{"regr_avgx", "FLOAT8", float64(2)},
		{"regr_avgy", "FLOAT8", float64(5)},
		{"regr_count", "INT8", int64(2)},
		{"regr_intercept", "FLOAT8", float64(1)},
		{"regr_r2", "FLOAT8", float64(1)},
		{"regr_slope", "FLOAT8", float64(2)},
		{"regr_sxx", "FLOAT8", float64(2)},
		{"regr_sxy", "FLOAT8", float64(4)},
		{"regr_syy", "FLOAT8", float64(8)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_recent_pairs(k INT,y DOUBLE PRECISION,x DOUBLE PRECISION)", 0))
				run(fixture.Exec("INSERT INTO og_recent_pairs VALUES(1,3,1),(2,7,3),(3,NULL,1000),(4,1000,NULL),(5,NULL,NULL)", 5))
				run(fixture.Exec("GRANT SELECT ON og_recent_pairs TO og_object_reader", 0))
				var empty any
				if tc.name == "regr_count" {
					empty = int64(0)
				}
				selectValue := func(q string, value any, args ...any) fixture.Step {
					return fixture.Select(q, []string{"v"}, []string{tc.typ}, [][]any{{value}}, args...)
				}
				call := "pg_catalog." + tc.name + "(y,x)"
				cases := []objectGrantProviderCase{
					{selectValue("SELECT "+call+" AS v FROM og_recent_pairs", tc.value), true, true},
					{selectValue("SELECT "+call+" AS v FROM og_recent_pairs WHERE k>99", empty), true, true},
					{selectValue("SELECT "+call+" AS v FROM og_recent_pairs WHERE FALSE", empty), true, true},
					{selectValue("SELECT "+call+" AS v FROM og_recent_pairs WHERE k>=3", empty), true, true},
					{selectValue("SELECT pg_catalog."+tc.name+"(y,x) FILTER(WHERE FALSE) AS v FROM og_recent_pairs", empty), true, true},
					{selectValue("SELECT pg_catalog."+tc.name+"(NULL::DOUBLE PRECISION,x) AS v FROM og_recent_pairs", empty), true, true},
					{selectValue("SELECT pg_catalog."+tc.name+"($1::DOUBLE PRECISION,x) AS v FROM og_recent_pairs", empty, nil), true, false},
					{fixture.Select("SELECT "+call+" AS v FROM og_recent_pairs WHERE FALSE GROUP BY k", []string{"v"}, []string{tc.typ}, nil), true, true},
					// Target CASE pruning is deliberately not assumed to erase a
					// surviving aggregate; root must establish this on PG18.
				}
				other, otherType, otherValue := "regr_count", "INT8", any(int64(2))
				if tc.name == "regr_count" {
					other, otherType, otherValue = "corr", "FLOAT8", float64(1)
				}
				controls := []fixture.Step{
					fixture.Select("SELECT "+other+"(y,x) AS v FROM og_recent_pairs", []string{"v"}, []string{otherType}, [][]any{{otherValue}}),
					fixture.Select("SELECT count(*) AS n FROM og_recent_pairs", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}),
				}
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog."+tc.name+"(double precision,double precision)", "aggregate", tc.name, cases, controls)
			})
		})
	}
}

func TestReferenceObjectGrantRecentNtileAuthority(t *testing.T) {
	objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
		run(fixture.Exec("CREATE TABLE og_recent_ntile(id INT,b INT)", 0))
		run(fixture.Exec("INSERT INTO og_recent_ntile VALUES(1,2),(2,0),(3,NULL),(4,-1),(5,99)", 5))
		run(fixture.Exec("GRANT SELECT ON og_recent_ntile TO og_object_reader", 0))
		labels := func(q string, values []any, args ...any) fixture.Step {
			rows := make([][]any, len(values))
			for i, v := range values {
				rows[i] = []any{int64(i + 1), v}
			}
			return fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, rows, args...)
		}
		cases := []objectGrantProviderCase{
			{labels("SELECT id,ntile(2) OVER(ORDER BY id) AS v FROM og_recent_ntile ORDER BY id", []any{int64(1), int64(1), int64(1), int64(2), int64(2)}), true, true},
			{labels("SELECT id,ntile(b) OVER(ORDER BY id) AS v FROM og_recent_ntile ORDER BY id", []any{int64(1), int64(1), int64(1), int64(2), int64(2)}), true, true},
			{labels("SELECT id,ntile($1::INTEGER) OVER(ORDER BY id) AS v FROM og_recent_ntile ORDER BY id", []any{int64(1), int64(1), int64(2), int64(2), int64(3)}, int64(3)), true, true},
			{labels("SELECT id,ntile(NULL::INTEGER) OVER(ORDER BY id) AS v FROM og_recent_ntile ORDER BY id", []any{nil, nil, nil, nil, nil}), true, true},
			{labels("SELECT id,ntile($1::INTEGER) OVER(ORDER BY id) AS v FROM og_recent_ntile ORDER BY id", []any{nil, nil, nil, nil, nil}, nil), true, false},
			{labels("SELECT id,ntile(0) OVER(ORDER BY id) AS v FROM og_recent_ntile WHERE id>99 ORDER BY id", nil), true, true},
			{labels("SELECT id,ntile(0) OVER(ORDER BY id) AS v FROM og_recent_ntile WHERE FALSE ORDER BY id", nil), true, true},
			{labels("SELECT id,ntile(0) OVER(ORDER BY id) AS v FROM og_recent_ntile ORDER BY id LIMIT 0", nil), true, true},
		}
		controls := []fixture.Step{
			fixture.Select("SELECT id,row_number() OVER(ORDER BY id) AS v FROM og_recent_ntile ORDER BY id", []string{"id", "v"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}, {int64(5), int64(5)}}),
		}
		objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog.ntile(integer)", "function", "ntile", cases, controls)
	})
}

func TestReferenceObjectGrantRecentMultirangeAuthority(t *testing.T) {
	for _, f := range []struct {
		rangeType, multiType, input, canonical string
	}{
		{"int4range", "INT4MULTIRANGE", "[1,4]", "{[1,5)}"},
		{"int8range", "INT8MULTIRANGE", "[1,4]", "{[1,5)}"},
		{"numrange", "NUMMULTIRANGE", "(1.00,2.000]", "{(1.00,2.000]}"},
	} {
		t.Run(f.rangeType, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_recent_range(id INT,r "+f.rangeType+")", 0))
				run(fixture.Exec("INSERT INTO og_recent_range VALUES(1,'"+f.input+"'),(2,'empty'),(3,NULL)", 3))
				run(fixture.Exec("GRANT SELECT ON og_recent_range TO og_object_reader", 0))
				// The pinned pq OID inventory predates multirange names.
				resultType := f.multiType
				if b.PG {
					resultType = ""
				}
				// Observe the existing range transport, not an unavailable
				// multirange-to-TEXT coercion.
				q := "SELECT id,multirange(r) AS v FROM og_recent_range ORDER BY id"
				parameter := "SELECT multirange(CASE WHEN TRUE THEN $1 ELSE r END) AS v FROM og_recent_range WHERE id=1"
				cases := []objectGrantProviderCase{
					{fixture.Select(q, []string{"id", "v"}, []string{"INT4", resultType}, [][]any{{int64(1), f.canonical}, {int64(2), "{}"}, {int64(3), nil}}), true, true},
					{fixture.Select("SELECT multirange(r) AS v FROM og_recent_range WHERE id>99", []string{"v"}, []string{resultType}, nil), true, true},
					{fixture.Select("SELECT multirange(r) AS v FROM og_recent_range WHERE id=3", []string{"v"}, []string{resultType}, [][]any{{nil}}), true, true},
					{fixture.Select(parameter, []string{"v"}, []string{resultType}, [][]any{{f.canonical}}, f.input), true, false},
					{fixture.Select(parameter, []string{"v"}, []string{resultType}, [][]any{{nil}}, nil), false, false},
					{fixture.Select("SELECT multirange(CASE WHEN TRUE THEN NULL ELSE r END) AS v FROM og_recent_range WHERE id=1", []string{"v"}, []string{resultType}, [][]any{{nil}}), false, false},
					{fixture.Select("SELECT multirange(r) AS v FROM og_recent_range WHERE FALSE", []string{"v"}, []string{resultType}, nil), true, true},
					{fixture.Select("SELECT CASE WHEN FALSE THEN multirange(r) ELSE NULL END AS v FROM og_recent_range WHERE id=1", []string{"v"}, []string{resultType}, [][]any{{nil}}), false, false},
					{fixture.Select("SELECT multirange(CASE WHEN TRUE THEN '"+f.input+"' ELSE r END) AS v FROM og_recent_range WHERE FALSE", []string{"v"}, []string{resultType}, nil), true, false},
				}
				for i, tc := range cases {
					rawColumn := len(tc.step.Columns) - 1
					cases[i].step.CheckRaw = func(t *testing.T, column, _ int, value any) bool {
						if column != rawColumn {
							return false
						}
						if value == nil {
							return true
						}
						if b.PG {
							raw, ok := value.([]byte)
							if !ok || raw == nil {
								t.Fatalf("multirange raw carrier = %T, want nonnil []byte", value)
							}
						} else if _, ok := value.(string); !ok {
							t.Fatalf("multirange raw carrier = %T, want string", value)
						}
						return true
					}
				}
				controls := []fixture.Step{
					fixture.Select("SELECT isempty(r) AS v FROM og_recent_range ORDER BY id", []string{"v"}, []string{"BOOL"}, [][]any{{false}, {true}, {nil}}),
					fixture.Select("SELECT upper('ok'::TEXT) AS v", []string{"v"}, []string{"TEXT"}, [][]any{{"OK"}}),
				}
				objectGrantProviderLifecycle(t, p, b, c, run, prepared, "pg_catalog.multirange(anyrange)", "function", "multirange", cases, controls)
			})
		})
	}
}
