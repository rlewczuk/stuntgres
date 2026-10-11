package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewNumericComparisonUsageSourceConsumerRoutes(t *testing.T) {
	for _, sizing := range []struct{ declaration, bound, good, stored, explicit, bad, badStored, overflowDetail string }{
		{"NUMERIC(4,2)", "1000", "1", "1.00", "10", "20", "20.00", "A field with precision 4, scale 2 must round to an absolute value less than 10^2."},
		{"NUMERIC(2,-1)", "10000", "149", "150", "200", "249", "250", "A field with precision 2, scale -1 must round to an absolute value less than 10^3."},
	} {
		for _, tc := range []struct {
			name, input, compact, pretty string
			rejects, overflow            bool
		}{
			{"less", "n<" + sizing.bound, "(n < (" + sizing.bound + ")::numeric)", "n < " + sizing.bound + "::numeric", false, false},
			{"reverse_less", sizing.bound + ">n", "((" + sizing.bound + ")::numeric > n)", sizing.bound + "::numeric > n", false, false},
			{"distinct", "n IS DISTINCT FROM " + sizing.bound, "(n IS DISTINCT FROM (" + sizing.bound + ")::numeric)", "n IS DISTINCT FROM " + sizing.bound + "::numeric", false, false},
			{"reverse_distinct", sizing.bound + " IS DISTINCT FROM n", "((" + sizing.bound + ")::numeric IS DISTINCT FROM n)", sizing.bound + "::numeric IS DISTINCT FROM n", false, false},
			{"false", "n>" + sizing.bound, "(n > (" + sizing.bound + ")::numeric)", "n > " + sizing.bound + "::numeric", true, false},
			{"reverse_false", sizing.bound + "<n", "((" + sizing.bound + ")::numeric < n)", sizing.bound + "::numeric < n", true, false},
			{"explicit_overflow", "n<CAST(" + sizing.bound + " AS " + sizing.declaration + ")", "(n < (" + sizing.bound + ")::" + numericReviewSpelling(sizing.declaration) + ")", "n < " + sizing.bound + "::" + numericReviewSpelling(sizing.declaration), false, true},
			{"explicit_success", "n<CAST(" + sizing.explicit + " AS " + sizing.declaration + ")", "(n < (" + sizing.explicit + ")::" + numericReviewSpelling(sizing.declaration) + ")", "n < " + sizing.explicit + "::" + numericReviewSpelling(sizing.declaration), false, false},
		} {
			t.Run(sizing.declaration+"/"+tc.name, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						db := b.DB
						db.SetMaxOpenConns(1)
						postgres := b.PG
						run := func(s fixture.Step) fixture.Observed {
							s.NonContext = true
							if s.SelectRows && s.Diagnostic == nil {
								previous := s.CheckMetadata
								s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
									fixture.DeparseReviewMetadata(t, b.PG, rows)
									deparseReviewNullableColumns(t, b.PG, rows, "n")
									if previous != nil {
										previous(t, rows)
									}
								}
							}
							return fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
						}
						run(fixture.Exec("CREATE TABLE original(id INT,n "+sizing.declaration+",CONSTRAINT c CHECK("+tc.input+"))", 0))
						for mode, expression := range []string{tc.compact, tc.pretty} {
							q := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d,pg_get_expr(conbin,conrelid,%t) AS e FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1, mode == 1)
							step := fixture.Select(q, []string{"d", "e"}, []string{"TEXT", "TEXT"}, [][]any{{"CHECK (" + expression + ")", expression}})
							var actual string
							if err := db.QueryRow(fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) FROM pg_catalog.pg_constraint WHERE conname='c'", mode == 1)).Scan(&actual); err != nil || actual != step.Rows[0][0] {
								t.Fatal(actual, step.Rows, err)
							}
							run(step)
							run(fixture.Prepared(step))
							run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(id INT,n %s,CONSTRAINT c%d %s)", mode, sizing.declaration, mode, actual), 0))
						}
						for i, name := range []string{"original", "copy0", "copy1"} {
							constraint := "c"
							if i > 0 {
								constraint = fmt.Sprintf("c%d", i-1)
							}
							for _, prepared := range []bool{false, true} {
								run(fixture.Exec("DELETE FROM "+name, 0))
								// DELETE has backend-independent affected counts from the previous frame.
								retained := [][]any{}
								if !tc.overflow {
									step := fixture.Exec("INSERT INTO "+name+" VALUES(1,NULL)", 1)
									step.Prepared = prepared
									run(step)
									retained = append(retained, []any{int64(1), nil})
									if !tc.rejects {
										step = fixture.Exec("INSERT INTO "+name+" VALUES(2,"+sizing.good+")", 1)
										step.Prepared = prepared
										run(step)
										retained = append(retained, []any{int64(2), sizing.stored})
										deparseReviewSourceTypedValue(t, db, postgres, "SELECT n FROM "+name+" WHERE id=2", sizing.declaration, sizing.stored)
										if sizing.declaration == "NUMERIC(4,2)" && tc.name != "explicit_success" {
											step = fixture.Exec("INSERT INTO "+name+" VALUES(6,99.99)", 1)
											step.Prepared = prepared
											run(step)
											retained = append(retained, []any{int64(6), "99.99"})
										}
									}
								}
								snapshot := func() {
									run(fixture.Select("SELECT id,n FROM "+name+" ORDER BY id", []string{"id", "n"}, []string{"INT4", "NUMERIC"}, retained))
								}
								snapshot()
								bad, badStored := sizing.good, sizing.stored
								if tc.name == "explicit_success" {
									bad, badStored = sizing.bad, sizing.badStored
								}
								if tc.overflow {
									for _, values := range []string{"(3," + bad + ")", "(3,NULL)", "(4,NULL),(3," + bad + "),(5,NULL)"} {
										step := fixture.ImplicitError("INSERT INTO "+name+" VALUES"+values, fixture.Diagnostic{Code: "22003", Message: "numeric field overflow", Detail: sizing.overflowDetail})
										step.Prepared = prepared
										run(step)
										snapshot()
									}
									step := fixture.Exec("UPDATE "+name+" SET n=1", 0)
									step.Prepared = prepared
									run(step)
								} else if tc.rejects || tc.name == "explicit_success" {
									for _, operation := range []struct{ query, id string }{
										{"INSERT INTO " + name + " VALUES(3," + bad + ")", "3"},
										{"INSERT INTO " + name + " VALUES(4,NULL),(3," + bad + "),(5,NULL)", "3"},
										{"UPDATE " + name + " SET n=" + bad + " WHERE id=1", "1"},
									} {
										step := fixture.ImplicitError(operation.query, fixture.CheckViolation(name, constraint, operation.id+", "+badStored))
										step.Prepared = prepared
										run(step)
										snapshot()
									}
								}
								rows, err := db.Query("SELECT n FROM " + name)
								if err != nil {
									t.Fatal(err)
								}
								types, err := rows.ColumnTypes()
								if err != nil || len(types) != 1 {
									rows.Close()
									t.Fatal(types, err)
								}
								p, s, ok := types[0].DecimalSize()
								precision, scale := int64(4), int64(2)
								if sizing.declaration == "NUMERIC(2,-1)" {
									precision, scale = 2, -1
									if postgres {
										scale = 2047
									}
								}
								// pq reports the encoded unsigned 11-bit scale; native retains -1.
								if !ok || p != precision || s != scale {
									rows.Close()
									t.Fatal(p, s, ok, precision, scale)
								}
								rows.Close()
								// Clear here so the next direct/prepared frame starts identically.
								run(fixture.Exec("DELETE FROM "+name, int64(len(retained))))
							}
						}
					})
				}
			})
		}
	}
}
