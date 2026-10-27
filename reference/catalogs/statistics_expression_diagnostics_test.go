package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceStatisticsExpressionDiagnostics(t *testing.T) {
	withNDistinct(t, func(t *testing.T, s ndistinctScenario) {
		s.run(fixture.Exec("CREATE TABLE expr_t(a INT,b INT,v INT GENERATED ALWAYS AS (a+b) VIRTUAL,w xid)", 0))
		s.run(fixture.Exec("CREATE STATISTICS expr_sentinel ON (a+1) FROM expr_t", 0))
		for _, tc := range []struct{ expression, kinds, code, message, token string }{
			{"(a+1)", "(ndistinct)", "0A000", "when building statistics on a single expression, statistics kinds may not be specified", ""},
			{"(a+1)", "(bogus)", "0A000", "when building statistics on a single expression, statistics kinds may not be specified", ""},
			{"(a)", "", "42P17", "extended statistics require at least 2 columns", ""},
			{"(expr_t.a)", "", "42P17", "extended statistics require at least 2 columns", ""},
			{"(a+1),(a+1)", "", "42701", "duplicate expression in statistics definition", ""},
			{"a,a,(b+1)", "", "42701", "duplicate column name in statistics definition", ""},
			{"(a+1),b", "(bogus)", "42601", `unrecognized statistics kind "bogus"`, ""},
			{"(a+1),(a+2),(a+3),(a+4),(a+5),(a+6),(a+7),(a+8),(a+9)", "", "54011", "cannot have more than 8 columns in statistics", ""},
			{"((SELECT 1))", "", "0A000", "cannot use subquery in statistics expression", "(SELECT"},
			{"(sum(a))", "", "42803", "aggregate functions are not allowed in statistics expressions", "sum"},
			{"(grouping(a))", "", "42803", "grouping operations are not allowed in statistics expressions", "grouping"},
			{"(row_number() OVER ())", "", "42P20", "window functions are not allowed in statistics expressions", "row_number"},
			{"(generate_series(1,2))", "", "0A000", "set-returning functions are not allowed in statistics expressions", "generate_series"},
			{"(tableoid::int+1)", "", "0A000", "statistics creation on system columns is not supported", ""},
			{"(v+1)", "", "0A000", "statistics creation on virtual generated columns is not supported", ""},
			{"(missing+1)", "", "42703", `column "missing" does not exist`, "missing"},
			{"(other.a+1)", "", "42P01", `missing FROM-clause entry for table "other"`, "other"},
			{"(coalesce(w,w)),b", "", "0A000", "expression cannot be used in multivariate statistics because its type xid has no default btree operator class", ""},
		} {
			// Multibyte identifier makes parser positions character-based, not byte offsets.
			q := `CREATE STATISTICS "é_bad" ` + tc.kinds + " ON " + tc.expression + " FROM expr_t"
			want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
			if tc.token != "" {
				offset := strings.Index(q, tc.token)
				want.Position = strconv.Itoa(utf8.RuneCountInString(q[:offset]) + 1)
			}
			// Isolate every witness so one discrepancy cannot hide later oracle
			// diagnostics. Both routes use the same independently specified record.
			for _, route := range []string{"direct", "prepared"} {
				t.Run(tc.kinds+tc.expression+"/"+route, func(t *testing.T) {
					recorder := s.pair.MemoryRecord
					if s.pg {
						recorder = s.pair.PostgresRecord
					}
					step := fixture.ErrorDiagnostic(q, want)
					if route == "prepared" {
						step = fixture.Prepared(step)
					}
					fixture.RunStep(t, s.pair.Ctx, s.conn, recorder, step)
				})
			}
			s.run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxrelid='expr_t'::regclass ORDER BY stxname", []string{"stxname"}, []string{"NAME"}, [][]any{{"expr_sentinel"}}))
		}
	})
}

func TestReferenceStatisticsExpressionNonowner(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"expr_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(step fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, step) }
			for _, q := range []string{"CREATE ROLE expr_reader", "GRANT USAGE,CREATE ON SCHEMA public TO expr_reader", "CREATE TABLE expr_t(a INT,b INT)", "GRANT SELECT ON expr_t TO expr_reader", "CREATE STATISTICS expr_single ON (a+1) FROM expr_t", "INSERT INTO expr_t VALUES(1,1),(1,1)", "ANALYZE expr_t", "SET ROLE expr_reader"} {
				count := int64(0)
				if strings.HasPrefix(q, "INSERT") {
					count = 2
				}
				run(fixture.Exec(q, count))
			}
			for _, prepared := range []bool{false, true} {
				step := statisticsError("CREATE STATISTICS expr_denied ON (a+1) FROM expr_t", "42501", "must be owner of table expr_t")
				if prepared {
					step = fixture.Prepared(step)
				}
				run(step)
			}
			run(fixture.Exec("RESET ROLE", 0))
			run(expressionDefinition("expr_single", "", "{e}"))
			run(fixture.Select("SELECT (stxdexpr[1]).stanullfrac,(stxdexpr[1]).stadistinct,(stxdexpr[1]).stavalues1::text AS values"+expressionDataFrom, []string{"stanullfrac", "stadistinct", "values"}, []string{"FLOAT4", "FLOAT4", "TEXT"}, [][]any{{float64(0), float64(-0.5), "{2}"}}, "expr_single"))
			run(fixture.Select("SELECT stxname FROM pg_statistic_ext WHERE stxname='expr_denied'", []string{"stxname"}, []string{"NAME"}, nil))
		})
	}
}
