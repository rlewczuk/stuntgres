package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceQueryTableCreateSetConsumers(t *testing.T) {
	one := []any{int64(1), "a"}
	two := []any{int64(2), nil}
	three := []any{int64(3), nil}
	null := []any{nil, "z"}
	cases := []struct {
		operator string
		rows     [][]any
	}{
		{"UNION", [][]any{one, two, three, null}},
		{"UNION DISTINCT", [][]any{one, two, three, null}},
		{"UNION ALL", [][]any{one, one, one, one, one, two, three, null, null, null}},
		{"INTERSECT", [][]any{one, null}},
		{"INTERSECT DISTINCT", [][]any{one, null}},
		{"INTERSECT ALL", [][]any{one, one, null}},
		{"EXCEPT", [][]any{two}},
		{"EXCEPT DISTINCT", [][]any{two}},
		{"EXCEPT ALL", [][]any{one, two, null}},
	}
	for _, tc := range cases {
		for _, noData := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				name := tc.operator + map[bool]string{false: "/data", true: "/no_data"}[noData] + map[bool]string{false: "/direct", true: "/prepared"}[prepared]
				t.Run(name, func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, b := range p.backends() {
						t.Run(b.name, func(t *testing.T) {
							_, run := queryTableCreateSession(t, p, b)
							run(referenceExec("CREATE TABLE qc_set_left(i INT,s TEXT)", 0))
							run(referenceExec("CREATE TABLE qc_set_right(i INT,s TEXT)", 0))
							run(referenceExec("INSERT INTO qc_set_left VALUES(1,'a'),(1,'a'),(1,'a'),(2,NULL),(NULL,'z'),(NULL,'z')", 6))
							run(referenceExec("INSERT INTO qc_set_right VALUES(1,'a'),(1,'a'),(3,NULL),(NULL,'z')", 4))
							q := "CREATE TABLE qc_set_copy(i) AS SELECT i AS left_i,s AS left_s FROM qc_set_left " + tc.operator + " SELECT i AS right_i,s AS right_s FROM qc_set_right"
							rows := tc.rows
							if noData {
								q += " WITH NO DATA"
								rows = nil
							}
							step := referenceExec(q, int64(len(rows)))
							step.prepared = prepared
							run(step)
							// Destination rows and labels remain independent of both source arms.
							run(referenceExec("DROP TABLE qc_set_left", 0))
							run(referenceExec("DROP TABLE qc_set_right", 0))
							result := referenceSelect("SELECT * FROM qc_set_copy", []string{"i", "left_s"}, []string{"INT4", "TEXT"}, rows)
							result.bag = true
							result.checkMetadata = func(t *testing.T, cursor *sql.Rows) {
								columns, err := cursor.ColumnTypes()
								if err != nil || len(columns) != 2 {
									t.Fatal(columns, err)
								}
								referenceTableDDLColumnTypes(t, b.pg, columns, result.columns, result.types, []bool{true, true})
							}
							run(referencePrepared(result))
							run(referenceSelect("SELECT count(*) AS n FROM qc_set_copy", []string{"n"}, []string{"INT8"}, [][]any{{int64(len(rows))}}))
							run(referenceExec("INSERT INTO qc_set_copy VALUES(NULL,NULL)", 1))
							run(referenceSelect("SELECT count(*) AS n FROM qc_set_copy", []string{"n"}, []string{"INT8"}, [][]any{{int64(len(rows) + 1)}}))
						})
					}
				})
			}
		}
	}
}

func TestReferenceQueryTableCreateSetTypes(t *testing.T) {
	cases := []struct {
		name, body, typ string
		rows            [][]any
	}{
		{"unknown", "SELECT NULL AS x UNION ALL SELECT 7", "INT4", [][]any{{nil}, {int64(7)}}},
		{"pairwise", "SELECT NULL AS x UNION (SELECT NULL UNION SELECT 1)", "INT4", [][]any{{nil}, {int64(1)}}},
		{"real", "SELECT 1::real AS x UNION ALL SELECT 2", "FLOAT4", [][]any{{float64(1)}, {float64(2)}}},
		{"values", "(VALUES(1),(1)) UNION ALL VALUES(2)", "INT4", [][]any{{int64(1)}, {int64(1)}, {int64(2)}}},
		{"bounds", "(SELECT 1 AS x UNION ALL SELECT 2 ORDER BY x LIMIT 1) UNION ALL SELECT 3 ORDER BY 1 DESC LIMIT 1", "INT4", [][]any{{int64(3)}}},
	}
	for _, tc := range cases {
		for _, noData := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{false: "/data", true: "/no_data"}[noData], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						_, run := queryTableCreateSession(t, p, b)
						q := "CREATE TABLE qc_set_type(x) AS " + tc.body
						rows := tc.rows
						if noData {
							q += " WITH NO DATA"
							rows = nil
						}
						run(referencePrepared(referenceExec(q, int64(len(rows)))))
						result := referenceSelect("SELECT x FROM qc_set_type", []string{"x"}, []string{tc.typ}, rows)
						result.bag = true
						run(result)
					})
				}
			})
		}
	}
}

func TestReferenceQueryTableCreateSetCollation(t *testing.T) {
	for _, noData := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			for _, prepared := range []bool{false, true} {
				name := map[bool]string{false: "implicit", true: "explicit"}[explicit] + map[bool]string{false: "/data", true: "/no_data"}[noData] + map[bool]string{false: "/direct", true: "/prepared"}[prepared]
				t.Run(name, func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, b := range p.backends() {
						t.Run(b.name, func(t *testing.T) {
							_, run := queryTableCreateSession(t, p, b)
							run(referenceExec(`CREATE TABLE qc_set_collation(a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`, 0))
							run(referenceExec("INSERT INTO qc_set_collation VALUES('c','p')", 1))
							left := "a"
							if explicit {
								left += ` COLLATE "C"`
							}
							q := "CREATE TABLE qc_set_collated AS SELECT " + left + " AS s FROM qc_set_collation UNION ALL SELECT b FROM qc_set_collation"
							rows := [][]any{{"c"}, {"p"}}
							if noData {
								q += " WITH NO DATA"
								rows = nil
							}
							if !explicit {
								want := controlsReferenceDiagnostic("42P22", `no collation was derived for column "s" with collatable type text`)
								want.Hint = "Use the COLLATE clause to set the collation explicitly."
								step := referenceErrorDiagnostic(q, want)
								step.prepared = prepared
								step.errorStage = "execute"
								run(step)
								queryTableCreateAbsent(t, run, b.pg, "qc_set_collated")
							} else {
								step := referenceExec(q, int64(len(rows)))
								step.prepared = prepared
								run(step)
								run(referenceSelect("SELECT s FROM qc_set_collated ORDER BY s", []string{"s"}, []string{"TEXT"}, rows))
								// Genuine catalog companion; native pg_attribute remains excluded.
								if b.pg {
									run(referenceSelect("SELECT c.collname::text AS c FROM pg_attribute a JOIN pg_collation c ON c.oid=a.attcollation WHERE a.attrelid='qc_set_collated'::regclass AND a.attname='s'", []string{"c"}, []string{"TEXT"}, [][]any{{"C"}}))
								}
							}
							run(referenceSelect("SELECT a,b FROM qc_set_collation", []string{"a", "b"}, []string{"TEXT", "TEXT"}, [][]any{{"c", "p"}}))
						})
					}
				})
			}
		}
	}
}

func TestReferenceQueryTableCreateSetAtomicity(t *testing.T) {
	for _, noData := range []bool{false, true} {
		for _, prepared := range []bool{false, true} {
			t.Run(map[bool]string{false: "data", true: "no_data"}[noData]+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						_, run := queryTableCreateSession(t, p, b)
						run(referenceExec("CREATE TABLE qc_set_failure_source(i INT)", 0))
						run(referenceExec("INSERT INTO qc_set_failure_source VALUES(1),(2)", 2))
						q := "CREATE TABLE qc_set_atomic AS SELECT 1 AS i UNION ALL SELECT 10/(i-2) FROM qc_set_failure_source"
						if noData {
							step := referenceExec(q+" WITH NO DATA", 0)
							step.prepared = prepared
							run(step)
							run(referenceSelect("SELECT i FROM qc_set_atomic", []string{"i"}, []string{"INT4"}, nil))
						} else {
							step := referenceErrorDiagnostic(q, controlsReferenceDiagnostic("22012", "division by zero"))
							step.prepared = prepared
							step.errorStage = "execute"
							run(step)
							queryTableCreateAbsent(t, run, b.pg, "qc_set_atomic")
						}
						run(referenceSelect("SELECT i FROM qc_set_failure_source ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
						run(referenceSelect("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
					})
				}
			})
		}
	}
}
