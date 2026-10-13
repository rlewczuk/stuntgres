package reference_test

import (
	"bytes"
	"stuntgres/reference/fixture"
	"testing"
)

// Release18 refresh_by_match_merge uses every eligible index's key equality
// AND whole-row image equality. A matched old row survives once even if more
// than one nullable new row matches it. Unmatched new occurrences all survive
// the diff and must satisfy every unique index. INCLUDE is not a key qual.
func concurrentNullMatching(t *testing.T) {
	for _, tc := range []struct {
		name, seed, insert, indexes string
		replace                     bool
		inserts                     int64
		old, want                   [][]any
		violation                   bool
	}{
		{"matching_old", "(1,NULL)", "(1,NULL)", "CREATE UNIQUE INDEX mv_u ON mv(a)", false, 1, [][]any{{int64(1), nil}}, [][]any{{int64(1), nil}}, false},
		{"matching_old_include", "(1,NULL)", "(1,NULL)", "CREATE UNIQUE INDEX mv_u ON mv(a) INCLUDE(b)", false, 1, [][]any{{int64(1), nil}}, [][]any{{int64(1), nil}}, false},
		{"matching_old_plus_unmatched", "(1,NULL)", "(1,20)", "CREATE UNIQUE INDEX mv_u ON mv(a)", false, 1, [][]any{{int64(1), nil}}, nil, true},
		{"unmatched_null_duplicates", "(1,10)", "(1,NULL),(1,NULL)", "CREATE UNIQUE INDEX mv_u ON mv(a)", true, 2, [][]any{{int64(1), int64(10)}}, nil, true},
		{"empty_old_null_duplicates", "", "(1,NULL),(1,NULL)", "CREATE UNIQUE INDEX mv_u ON mv(a)", false, 2, nil, nil, true},
		{"all_eligible_keys", "(1,NULL)", "(1,NULL)", "CREATE UNIQUE INDEX mv_u ON mv(a); CREATE UNIQUE INDEX mv_ab_u ON mv(a,b)", false, 1, [][]any{{int64(1), nil}}, nil, true},
		{"composite_null_key_only", "(1,NULL)", "(1,NULL)", "CREATE UNIQUE INDEX mv_u ON mv(a,b)", false, 1, [][]any{{int64(1), nil}}, [][]any{{int64(1), nil}, {int64(1), nil}}, false},
		{"partial_key_not_qual", "(1,NULL)", "(1,NULL)", "CREATE UNIQUE INDEX mv_u ON mv(a); CREATE UNIQUE INDEX mv_ab_u ON mv(a,b) WHERE a>0", false, 1, [][]any{{int64(1), nil}}, [][]any{{int64(1), nil}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					conn, run := fixture.QueryTableCreateSession(t, p, b)
					run(fixture.Exec("CREATE TABLE dup_base(a INT,b INT)", 0))
					initial := int64(len(tc.old))
					if tc.seed != "" {
						run(fixture.Exec("INSERT INTO dup_base VALUES"+tc.seed, initial))
					}
					run(fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT a,b FROM dup_base", initial))
					run(fixture.Exec(tc.indexes, 0))
					bag := func(values [][]any) fixture.Step {
						s := fixture.Select("SELECT a,b FROM mv", []string{"a", "b"}, []string{"INT4", "INT4"}, values)
						s.Bag = true
						return s
					}
					run(bag(tc.old))
					baseCount := initial
					if tc.replace {
						run(fixture.Exec("DELETE FROM dup_base", initial))
						baseCount = 0
					}
					run(fixture.Exec("INSERT INTO dup_base VALUES"+tc.insert, tc.inserts))
					baseCount += tc.inserts
					var before []byte
					if !b.PG && tc.violation {
						before = concurrentDump(t, p)
					}
					for _, prepared := range []bool{false, true} {
						q := "REFRESH MATERIALIZED VIEW CONCURRENTLY mv"
						if tc.violation {
							d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "mv_u"`)
							d.Detail = "Key (a)=(1) already exists."
							d.Schema = "public"
							d.Table = "mv"
							d.Constraint = "mv_u"
							s := fixture.ErrorDiagnostic(q, d)
							s.Prepared = prepared
							s.ErrorStage = "execute"
							concurrentRuntimeFailure(t, p, b, conn, s)
							run(bag(tc.old))
							if !b.PG && !bytes.Equal(before, concurrentDump(t, p)) {
								t.Fatal("failed unmatched NULL refresh changed Dump")
							}
						} else {
							s := fixture.Exec(q, 0)
							s.Prepared = prepared
							run(s)
							run(bag(tc.want))
						}
						fixture.MaterializedViewPopulation(run, "mv", true)
					}
					run(fixture.Exec("DELETE FROM dup_base", baseCount))
					run(fixture.Exec("INSERT INTO dup_base VALUES(1,30)", 1))
					run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY mv", 0))
					run(bag([][]any{{int64(1), int64(30)}}))
				})
			}
		})
	}
}

func concurrentNullImages(t *testing.T) {
	for _, image := range []struct{ name, typ, seed, changed, oldText, newText string }{
		{"numeric_scale", "NUMERIC", "1.0", "1.00", "1.0", "1.00"},
		{"signed_zero", "DOUBLE PRECISION", "'0'", "'-0'", "0", "-0"},
	} {
		for _, mode := range []string{"same_image_match", "old_match_plus_changed_image", "unmatched_changed_duplicates", "single_changed_image"} {
			t.Run(image.name+"/"+mode, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						conn, run := fixture.QueryTableCreateSession(t, p, b)
						run(fixture.Exec("CREATE TABLE dup_base(a INT,b "+image.typ+",c INT)", 0))
						run(fixture.Exec("INSERT INTO dup_base VALUES(1,"+image.seed+",NULL)", 1))
						run(fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT a,b,c FROM dup_base", 1))
						run(fixture.Exec("CREATE UNIQUE INDEX mv_u ON mv(a) INCLUDE(b,c)", 0))
						projection, datatype := "b::text AS b", "TEXT"
						if image.name == "numeric_scale" {
							projection, datatype = "b", "NUMERIC"
						}
						bag := func(text string) fixture.Step {
							return fixture.Select("SELECT a,"+projection+",c FROM mv", []string{"a", "b", "c"}, []string{"INT4", datatype, "INT4"}, [][]any{{int64(1), text, nil}})
						}
						run(bag(image.oldText))
						baseCount := int64(1)
						if mode == "unmatched_changed_duplicates" || mode == "single_changed_image" {
							run(fixture.Exec("DELETE FROM dup_base", 1))
							baseCount = 0
						}
						value := image.changed
						if mode == "same_image_match" {
							value = image.seed
						}
						count := int64(1)
						values := "(1," + value + ",NULL)"
						if mode == "unmatched_changed_duplicates" {
							values += ",(1," + value + ",NULL)"
							count = 2
						}
						run(fixture.Exec("INSERT INTO dup_base VALUES"+values, count))
						baseCount += count
						violation := mode == "old_match_plus_changed_image" || mode == "unmatched_changed_duplicates"
						var before []byte
						if !b.PG && violation {
							before = concurrentDump(t, p)
						}
						for _, prepared := range []bool{false, true} {
							q := "REFRESH MATERIALIZED VIEW CONCURRENTLY mv"
							if violation {
								d := fixture.ControlsReferenceDiagnostic("23505", `duplicate key value violates unique constraint "mv_u"`)
								d.Detail = "Key (a)=(1) already exists."
								d.Schema = "public"
								d.Table = "mv"
								d.Constraint = "mv_u"
								s := fixture.ErrorDiagnostic(q, d)
								s.Prepared = prepared
								s.ErrorStage = "execute"
								concurrentRuntimeFailure(t, p, b, conn, s)
								run(bag(image.oldText))
								if !b.PG && !bytes.Equal(before, concurrentDump(t, p)) {
									t.Fatal("failed image refresh changed Dump")
								}
							} else {
								s := fixture.Exec(q, 0)
								s.Prepared = prepared
								run(s)
								text := image.oldText
								if mode == "single_changed_image" {
									text = image.newText
								}
								run(bag(text))
							}
							fixture.MaterializedViewPopulation(run, "mv", true)
						}
						run(fixture.Exec("DELETE FROM dup_base", baseCount))
						run(fixture.Exec("INSERT INTO dup_base VALUES(1,"+image.seed+",NULL)", 1))
						run(fixture.Exec("REFRESH MATERIALIZED VIEW CONCURRENTLY mv", 0))
						run(bag(image.oldText))
					})
				}
			})
		}
	}
}
