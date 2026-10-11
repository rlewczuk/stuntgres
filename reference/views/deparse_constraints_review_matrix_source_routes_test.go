package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewMatrixSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewChecks {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					db := b.DB
					db.SetMaxOpenConns(1)
					run := func(s fixture.Step) fixture.Observed {
						s.NonContext = true
						if s.SelectRows && s.Diagnostic == nil {
							previous := s.CheckMetadata
							s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
								fixture.DeparseReviewMetadata(t, b.PG, rows)
								if previous != nil {
									previous(t, rows)
								}
							}
						}
						return fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
					}
					run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
					run(fixture.Exec("SET DateStyle TO 'ISO, MDY'", 0))
					run(fixture.Exec("SET IntervalStyle TO 'postgres'", 0))
					run(fixture.Exec("CREATE SEQUENCE dc_cov_s START 40", 0))
					run(fixture.Exec("CREATE TABLE original(a INT,b INT,flag BOOL,CONSTRAINT c CHECK("+tc.input+"))", 0))
					for i, want := range []string{tc.compact, tc.pretty} {
						query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", i == 1)
						s := fixture.Select(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}})
						var suffix string
						if err := db.QueryRow(s.Query, s.Args...).Scan(&suffix); err != nil || suffix != s.Rows[0][0] {
							t.Fatal(suffix, s.Rows, err)
						}
						run(s)
						run(fixture.Prepared(s))
						run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(a INT,b INT,flag BOOL,CONSTRAINT copied %s)", i, suffix), 0))
					}
					run(fixture.Select("SELECT nextval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}}))
					for _, name := range []string{"original", "copy0", "copy1"} {
						run(fixture.Exec("INSERT INTO "+name+" VALUES(1,1,true)", 1))
					}
					switch tc.name {
					case "cov_next":
						run(fixture.Select("SELECT currval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(43)}}))
					case "cov_curr", "cov_last":
						run(fixture.Select("SELECT currval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(40)}}))
					case "cov_set2", "cov_set3true":
						run(fixture.Select("SELECT nextval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(2)}}))
					case "cov_set3false":
						run(fixture.Select("SELECT nextval('dc_cov_s') AS v", []string{"v"}, []string{"INT8"}, [][]any{{int64(1)}}))
					}
				})
			}
		})
	}
}

func TestReferenceDeparseConstraintsReviewDefaultCopiesSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewDefaults {
		t.Run(tc.name, func(t *testing.T) {
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
								if previous != nil {
									previous(t, rows)
								}
							}
						}
						return fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
					}
					run(fixture.Exec("SET TIME ZONE 'UTC'", 0))
					run(fixture.Exec("SET DateStyle TO 'ISO, MDY'", 0))
					run(fixture.Exec("SET IntervalStyle TO 'postgres'", 0))
					run(fixture.Exec("CREATE TABLE original(id INT,v "+tc.declaration+" DEFAULT "+tc.input+")", 0))
					for i := 0; i < 2; i++ {
						query := fmt.Sprintf("SELECT pg_get_expr(d.adbin,0,%t) AS d FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class c ON d.adrelid=c.oid WHERE c.relname='original'", i == 1)
						s := fixture.Select(query, []string{"d"}, []string{"TEXT"}, [][]any{{tc.golden}})
						var suffix string
						if err := db.QueryRow(s.Query, s.Args...).Scan(&suffix); err != nil || suffix != s.Rows[0][0] {
							t.Fatal(suffix, s.Rows, err)
						}
						run(s)
						run(fixture.Prepared(s))
						run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(id INT,v %s DEFAULT %s)", i, tc.declaration, suffix), 0))
					}
					for _, name := range []string{"original", "copy0", "copy1"} {
						run(fixture.Exec("INSERT INTO "+name+"(id) VALUES(1)", 1))
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
