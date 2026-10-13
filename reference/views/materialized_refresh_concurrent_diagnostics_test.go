package reference_test

import (
	"bytes"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedRefreshConcurrentDiagnostics(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "Refresh Schema")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			concurrentSeed(run)
			run(fixture.Exec("CREATE MATERIALIZED VIEW concurrent_no AS SELECT i,s FROM concurrent_base WITH NO DATA", 0))
			run(fixture.Exec("CREATE UNIQUE INDEX concurrent_no_u ON concurrent_no(i)", 0))
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct {
					q string
					d fixture.Diagnostic
				}{
					{concurrentRefreshSQL, concurrentNoIndex("concurrent_mv")},
					{concurrentRefreshSQL + " WITH NO DATA", fixture.ControlsReferenceDiagnostic("42601", "CONCURRENTLY and WITH NO DATA options cannot be used together")},
					{"REFRESH MATERIALIZED VIEW CONCURRENTLY concurrent_no", fixture.ControlsReferenceDiagnostic("0A000", "CONCURRENTLY cannot be used when the materialized view is not populated")},
					{"REFRESH MATERIALIZED VIEW CONCURRENTLY concurrent_no WITH NO DATA", fixture.ControlsReferenceDiagnostic("0A000", "CONCURRENTLY cannot be used when the materialized view is not populated")},
					{"REFRESH MATERIALIZED VIEW CONCURRENTLY missing", fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)},
				} {
					s := fixture.ErrorDiagnostic(tc.q, tc.d)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
			}
			concurrentIndex(run)
			run(fixture.ErrorDiagnostic(concurrentRefreshSQL+" WITH NO DATA", fixture.ControlsReferenceDiagnostic("42601", "CONCURRENTLY and WITH NO DATA options cannot be used together")))
			run(concurrentOld())
			fixture.MaterializedViewPopulation(run, "concurrent_mv", true)
			run(fixture.Exec(`CREATE SCHEMA "Refresh Schema"`, 0))
			run(fixture.Exec(`CREATE MATERIALIZED VIEW "Refresh Schema"."Odd View" AS SELECT i FROM concurrent_base`, 2))
			d := fixture.ControlsReferenceDiagnostic("55000", `cannot refresh materialized view ""Refresh Schema"."Odd View"" concurrently`)
			d.Hint = "Create a unique index with no WHERE clause on one or more columns of the materialized view."
			run(fixture.ErrorDiagnostic(`REFRESH MATERIALIZED VIEW CONCURRENTLY "Refresh Schema"."Odd View"`, d))
			run(fixture.Exec(`DROP SCHEMA "Refresh Schema" CASCADE`, 0))
		})
	}
}

func TestReferenceMaterializedRefreshConcurrentEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, ddl string
		eligible  bool
	}{
		{"absent", "", false},
		{"nonunique", "CREATE INDEX concurrent_u ON concurrent_mv(i)", false},
		{"partial", "CREATE UNIQUE INDEX concurrent_u ON concurrent_mv(i) WHERE i>0", false},
		{"expression", "CREATE UNIQUE INDEX concurrent_u ON concurrent_mv((i+1))", false},
		{"simple", "CREATE UNIQUE INDEX concurrent_u ON concurrent_mv(i)", true},
		{"composite", "CREATE UNIQUE INDEX concurrent_u ON concurrent_mv(i,s)", true},
		{"include", "CREATE UNIQUE INDEX concurrent_u ON concurrent_mv(i) INCLUDE(s)", true},
		{"descending", "CREATE UNIQUE INDEX concurrent_u ON concurrent_mv(i DESC NULLS FIRST)", true},
		{"not_distinct", "CREATE UNIQUE INDEX concurrent_u ON concurrent_mv(i) NULLS NOT DISTINCT", true},
		{"mixed", "CREATE UNIQUE INDEX concurrent_partial ON concurrent_mv(i) WHERE i>0; CREATE UNIQUE INDEX concurrent_u ON concurrent_mv(i)", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					_, run := fixture.QueryTableCreateSession(t, p, b)
					concurrentSeed(run)
					// Unique-expression creation is an existing native boundary, not new scope.
					if tc.name == "expression" && !b.PG {
						d := fixture.ControlsReferenceDiagnostic("0A000", "unique expression indexes are unsupported")
						d.Position = "51"
						run(fixture.ErrorDiagnostic(tc.ddl, d))
						return
					}
					if tc.ddl != "" {
						run(fixture.Exec(tc.ddl, 0))
					}
					concurrentChange(run)
					if tc.eligible {
						run(fixture.Exec(concurrentRefreshSQL, 0))
						run(concurrentNew())
					} else {
						run(fixture.ErrorDiagnostic(concurrentRefreshSQL, concurrentNoIndex("concurrent_mv")))
						run(concurrentOld())
					}
				})
			}
		})
	}
	// Default NULL-distinct index admits duplicate rows whose key is NULL.
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name+"_null_distinct", func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			concurrentSeed(run)
			concurrentIndex(run)
			run(fixture.Exec("INSERT INTO concurrent_base VALUES(NULL,'same'),(NULL,'same')", 2))
			for range 2 {
				run(fixture.Exec(concurrentRefreshSQL, 0))
				run(refreshBag("concurrent_mv", [][]any{{int64(1), "old"}, {int64(2), nil}, {nil, "same"}, {nil, "same"}}))
			}
		})
	}
}

func concurrentDump(t *testing.T, p *fixture.Pair) []byte {
	t.Helper()
	var saved bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &saved); err != nil {
		t.Fatal(err)
	}
	return bytes.Clone(saved.Bytes())
}

func TestReferenceMaterializedRefreshConcurrentDuplicates(t *testing.T) {
	for _, tc := range []struct{ name, typ, seed, insert, code, message, detail string }{
		{"identical", "INT", "10", "(1,10)", "21000", `new data for materialized view "mv" contains duplicate rows without any null columns`, "Row: (1,10)"},
		{"key_only", "INT", "10", "(1,20)", "23505", `duplicate key value violates unique constraint "mv_u"`, "Key (a)=(1) already exists."},
		{"group_A_B_A", "INT", "10", "(1,20),(1,10)", "21000", `new data for materialized view "mv" contains duplicate rows without any null columns`, "Row: (1,10)"},
		{"numeric_image", "NUMERIC", "1.0", "(1,1.00)", "23505", `duplicate key value violates unique constraint "mv_u"`, "Key (a)=(1) already exists."},
		{"float_image", "DOUBLE PRECISION", "'0'", "(1,'-0')", "23505", `duplicate key value violates unique constraint "mv_u"`, "Key (a)=(1) already exists."},
		{"json_image", "JSON", "'{\"x\":1}'", "(1,'{\"x\":1}')", "21000", `new data for materialized view "mv" contains duplicate rows without any null columns`, "Row: (1,\"{\"\"x\"\":1}\")"},
		{"bpchar_image", "CHAR(2)", "'a'", "(1,'a')", "21000", `new data for materialized view "mv" contains duplicate rows without any null columns`, "Row: (1,\"a \")"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, run := fixture.QueryTableCreateSession(t, p, b)
					run(fixture.Exec("CREATE TABLE dup_base(a INT,b "+tc.typ+")", 0))
					run(fixture.Exec("INSERT INTO dup_base VALUES(1,"+tc.seed+")", 1))
					run(fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT a,b FROM dup_base", 1))
					run(fixture.Exec("CREATE UNIQUE INDEX mv_u ON mv(a)", 0))
					before := map[string]string{"identical": "10", "key_only": "10", "group_A_B_A": "10", "numeric_image": "1.0", "float_image": "0", "json_image": `{"x":1}`, "bpchar_image": "a"}[tc.name]
					projection, datatype := "b::text AS b", "TEXT"
					if tc.name == "numeric_image" {
						projection, datatype = "b", "NUMERIC"
					}
					oldRows := fixture.Select("SELECT a,"+projection+" FROM mv", []string{"a", "b"}, []string{"INT4", datatype}, [][]any{{int64(1), before}})
					run(oldRows)
					n := 1
					if tc.name == "group_A_B_A" {
						n = 2
					}
					run(fixture.Exec("INSERT INTO dup_base VALUES"+tc.insert, int64(n)))
					var dump []byte
					if !b.PG {
						dump = concurrentDump(t, p)
					}
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Detail = tc.detail
					if tc.code == "23505" {
						d.Schema = "public"
						d.Table = "mv"
						d.Constraint = "mv_u"
					}
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW CONCURRENTLY mv", d)
						s.Prepared = prepared
						s.ErrorStage = "execute"
						if tc.code == "23505" {
							concurrentRuntimeFailure(t, p, b, conn, s)
						} else {
							run(s)
						}
						run(oldRows)
						fixture.MaterializedViewPopulation(run, "mv", true)
						if !b.PG && !bytes.Equal(dump, concurrentDump(t, p)) {
							t.Fatal("failed duplicate refresh changed Dump")
						}
					}
					run(fixture.Exec("DELETE FROM dup_base", int64(n+1)))
					run(fixture.Exec("INSERT INTO dup_base VALUES(1,"+tc.seed+")", 1))
					run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY mv", 0))
				})
			}
		})
	}
	t.Run("owner_disclosure", concurrentOwnerDisclosure)
	t.Run("multiple_indexes_duplicate_precedence", concurrentDuplicatePrecedence)
	t.Run("null_payload_matching", concurrentNullMatching)
	t.Run("partial_null_images", concurrentNullImages)
	t.Run("null_not_distinct", func(t *testing.T) {
		for _, tc := range []struct{ name, insert string }{{"different_payload", "(NULL,20)"}, {"same_image", "(NULL,10)"}} {
			t.Run(tc.name, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						conn, run := fixture.QueryTableCreateSession(t, p, b)
						run(fixture.Exec("CREATE TABLE dup_base(a INT,b INT)", 0))
						run(fixture.Exec("INSERT INTO dup_base VALUES(NULL,10)", 1))
						run(fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT a,b FROM dup_base", 1))
						run(fixture.Exec("CREATE UNIQUE INDEX mv_u ON mv(a) NULLS NOT DISTINCT", 0))
						run(fixture.Exec("INSERT INTO dup_base VALUES"+tc.insert, 1))
						d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "mv_u"`)
						d.Detail = "Key (a)=(null) already exists."
						d.Schema = "public"
						d.Table = "mv"
						d.Constraint = "mv_u"
						for _, prepared := range []bool{false, true} {
							s := fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW CONCURRENTLY mv", d)
							s.Prepared = prepared
							s.ErrorStage = "execute"
							concurrentRuntimeFailure(t, p, b, conn, s)
							run(fixture.Select("SELECT a,b FROM mv", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{nil, int64(10)}}))
						}
					})
				}
			})
		}
	})
}

func TestReferenceMaterializedRefreshConcurrentAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			_, run := fixture.QueryTableCreateSession(t, p, b)
			run(fixture.Exec("CREATE TABLE concurrent_fail_base(i INT)", 0))
			run(fixture.Exec("INSERT INTO concurrent_fail_base VALUES(1)", 1))
			run(fixture.Exec("CREATE MATERIALIZED VIEW concurrent_fail AS SELECT 1/i AS x FROM concurrent_fail_base", 1))
			run(fixture.Exec("CREATE UNIQUE INDEX concurrent_fail_u ON concurrent_fail(x)", 0))
			run(fixture.Exec("INSERT INTO concurrent_fail_base VALUES(0)", 1))
			var dump []byte
			if !b.PG {
				dump = concurrentDump(t, p)
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW CONCURRENTLY concurrent_fail", fixture.ControlsReferenceDiagnostic("22012", "division by zero"))
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT x FROM concurrent_fail", []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}}))
				fixture.MaterializedViewPopulation(run, "concurrent_fail", true)
				if !b.PG && !bytes.Equal(dump, concurrentDump(t, p)) {
					t.Fatal("failed producer changed Dump")
				}
			}
			run(fixture.Exec("DELETE FROM concurrent_fail_base WHERE i=0", 1))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY concurrent_fail", 0))
		})
	}
}
