package reference_test

import (
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

// Construction-only witnesses resolve universe membership independently of
// whether any standard comparator happens to exist. A native setup failure is
// a construction observation, never evidence that an admitted scalar pair may
// be skipped. Do not silently turn such failures into a passing comparison.
func TestReferenceComparisonStructuralConstruction(t *testing.T) {
	for _, tc := range []struct{ name, base, value string }{
		{"money", "money", "'1.00'"},
		{"tid", "tid", "'(1,2)'"},
		{"range", "int4range", "'[1,3)'"},
		{"xid", "xid", "'1'"},
		{"cid", "cid", "'1'"},
		{"xid8", "xid8", "'1'"},
	} {
		t.Run("domain_array_"+tc.name, func(t *testing.T) {
			comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				ddl := "CREATE DOMAIN comparison_element AS " + tc.base
				if !b.PG && (tc.name == "xid" || tc.name == "cid" || tc.name == "xid8") {
					_, err := b.DB.ExecContext(p.Ctx, ddl)
					if err == nil || err.Error() != "invalid array descriptor" {
						t.Fatalf("transaction-domain construction boundary: got %v", err)
					}
					var state interface{ SQLState() string }
					if errors.As(err, &state) {
						t.Fatalf("construction boundary unexpectedly has SQLSTATE %s", state.SQLState())
					}
					return
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(ddl, 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE comparison_domain_route(v comparison_element)", 0))
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO comparison_domain_route VALUES("+tc.value+")", 1))
				q := "SELECT cardinality(ARRAY[v]) AS n FROM comparison_domain_route"
				comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			})
		})
	}
	t.Run("enum_and_composite_expression_arrays", func(t *testing.T) {
		comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			for _, q := range []string{
				"CREATE TYPE comparison_enum AS ENUM ('a','b')",
				"CREATE TYPE comparison_record AS (x integer)",
				"CREATE DOMAIN comparison_array AS integer[]",
				"CREATE DOMAIN comparison_enum_domain AS comparison_enum",
			} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
			}
			for _, expr := range []string{
				"ARRAY['a'::comparison_enum]", "ARRAY[ROW(1)::comparison_record]",
				"ARRAY[1]::comparison_array", "ARRAY['a'::comparison_enum_domain]",
			} {
				comparisonCheckpointRoutes(t, p, b, fixture.Select("SELECT cardinality("+expr+") AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
			}
		})
	})
	t.Run("occurrence_record_array", func(t *testing.T) {
		comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			q := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path SELECT cardinality(path) AS depth FROM r ORDER BY depth"
			comparisonCheckpointRoutes(t, p, b, fixture.Select(q, []string{"depth"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
		})
	})
	t.Run("catalog_and_expression_only", func(t *testing.T) {
		comparisonCheckpoint(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE comparison_routes (x integer DEFAULT 1 CHECK (x > 0))", 0))
			for _, tc := range []struct {
				q    string
				want bool
			}{
				{"SELECT relkind = 'r'::text AS v FROM pg_class WHERE oid='comparison_routes'::regclass", true},
				{"SELECT conbin IS NOT NULL AS v FROM pg_constraint WHERE conrelid='comparison_routes'::regclass AND contype='c'", true},
				{"SELECT conbin = conbin AS v FROM pg_constraint WHERE conrelid='comparison_routes'::regclass AND contype='c'", true},
				{"SELECT conbin < conbin AS v FROM pg_constraint WHERE conrelid='comparison_routes'::regclass AND contype='c'", false},
				{"SELECT adbin IS NOT NULL AS v FROM pg_attrdef WHERE adrelid='comparison_routes'::regclass", true},
				{"SELECT attmissingval IS NULL AS v FROM pg_attribute WHERE attrelid='comparison_routes'::regclass AND attnum=1", true},
				{"SELECT pg_sleep(0) IS NULL AS v", false},
				{"SELECT NULL::record IS NULL AS v", true},
			} {
				t.Run(tc.q, func(t *testing.T) {
					comparisonCheckpointRoutes(t, p, b, fixture.Select(tc.q, []string{"v"}, []string{"BOOL"}, [][]any{{tc.want}}))
				})
			}
		})
	})
}
