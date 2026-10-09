package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewMatrixSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewChecks {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					db := b.db
					db.SetMaxOpenConns(1)
					run := func(s referenceStep) referenceObserved {
						s.nonContext = true
						if s.selectRows && s.diagnostic == nil {
							previous := s.checkMetadata
							s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
								deparseReviewMetadata(t, b.pg, rows)
								if previous != nil {
									previous(t, rows)
								}
							}
						}
						return referenceRunStep(t, p.ctx, db, b.recorder, s)
					}
					run(referenceExec("SET TIME ZONE 'UTC'", 0))
					run(referenceExec("SET DateStyle TO 'ISO, MDY'", 0))
					run(referenceExec("SET IntervalStyle TO 'postgres'", 0))
					run(referenceExec("CREATE SEQUENCE dc_cov_s START 40", 0))
					run(referenceExec("CREATE TABLE original(a INT,b INT,flag BOOL,CONSTRAINT c CHECK("+tc.input+"))", 0))
					for i, want := range []string{tc.compact, tc.pretty} {
						query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", i == 1)
						s := referenceSelect(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
						var suffix string
						if err := db.QueryRow(s.query, s.args...).Scan(&suffix); err != nil || suffix != s.rows[0][0] {
							t.Fatal(suffix, s.rows, err)
						}
						run(s)
						run(referencePrepared(s))
						run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(a INT,b INT,flag BOOL,CONSTRAINT copied %s)", i, suffix), 0))
					}
					run(referenceSelect("SELECT nextval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}}))
					for _, name := range []string{"original", "copy0", "copy1"} {
						run(referenceExec("INSERT INTO "+name+" VALUES(1,1,true)", 1))
					}
					switch tc.name {
					case "cov_next":
						run(referenceSelect("SELECT currval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(43)}}))
					case "cov_curr", "cov_last":
						run(referenceSelect("SELECT currval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}}))
					case "cov_set2", "cov_set3true":
						run(referenceSelect("SELECT nextval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}))
					case "cov_set3false":
						run(referenceSelect("SELECT nextval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
				})
			}
		})
	}
}

func TestReferenceDeparseConstraintsReviewDefaultCopiesSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewDefaults {
		t.Run(tc.name, func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					db := b.db
					db.SetMaxOpenConns(1)
					postgres := b.pg
					run := func(s referenceStep) referenceObserved {
						s.nonContext = true
						if s.selectRows && s.diagnostic == nil {
							previous := s.checkMetadata
							s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
								deparseReviewMetadata(t, b.pg, rows)
								if previous != nil {
									previous(t, rows)
								}
							}
						}
						return referenceRunStep(t, p.ctx, db, b.recorder, s)
					}
					run(referenceExec("SET TIME ZONE 'UTC'", 0))
					run(referenceExec("SET DateStyle TO 'ISO, MDY'", 0))
					run(referenceExec("SET IntervalStyle TO 'postgres'", 0))
					run(referenceExec("CREATE TABLE original(id INT,v "+tc.declaration+" DEFAULT "+tc.input+")", 0))
					for i := 0; i < 2; i++ {
						query := fmt.Sprintf("SELECT pg_get_expr(d.adbin,0,%t) AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='original'", i == 1)
						s := referenceSelect(query, []string{"d"}, []string{"TEXT"}, [][]any{{tc.golden}})
						var suffix string
						if err := db.QueryRow(s.query, s.args...).Scan(&suffix); err != nil || suffix != s.rows[0][0] {
							t.Fatal(suffix, s.rows, err)
						}
						run(s)
						run(referencePrepared(s))
						run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(id INT,v %s DEFAULT %s)", i, tc.declaration, suffix), 0))
					}
					for _, name := range []string{"original", "copy0", "copy1"} {
						run(referenceExec("INSERT INTO "+name+"(id) VALUES(1)", 1))
						deparseReviewSourceTypedValue(t, db, postgres, "SELECT v FROM "+name, tc.declaration, tc.value)
						if tc.name == "time_literal3" {
							var text string
							if err := db.QueryRow("SELECT v::text FROM " + name).Scan(&text); err != nil || text != "12:34:56.123" {
								t.Fatal(text, err)
							}
						}
					}
				})
			}
		})
	}
}
