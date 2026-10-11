package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewNestedBehaviorSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewBehavior {
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
					run(fixture.Exec("CREATE TABLE original(a INT,b INT,CONSTRAINT c CHECK("+tc.input+"))", 0))
					// No source expression substitution: collect actual suffixes directly through
					// the public driver; the exact renderer matrix independently checks goldens.
					for i := 0; i < 2; i++ {
						query := fmt.Sprintf("SELECT pg_get_constraintdef(oid,%t) AS d FROM pg_catalog.pg_constraint WHERE conname='c'", i == 1)
						// This scenario uses independently derived goldens for the extra predicates.
						compact, pretty := deparseReviewBehaviorGolden(tc.name)
						want := compact
						if i == 1 {
							want = pretty
						}
						var suffix string
						if err := db.QueryRow(query).Scan(&suffix); err != nil || suffix != want {
							t.Fatal(suffix, want, err)
						}
						run(fixture.Select(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}}))
						run(fixture.Exec(fmt.Sprintf("CREATE TABLE copy%d(a INT,b INT,CONSTRAINT c%d %s)", i, i, suffix), 0))
					}
					for i, name := range []string{"original", "copy0", "copy1"} {
						constraint := "c"
						if i > 0 {
							constraint = fmt.Sprintf("c%d", i-1)
						}
						var good any
						if tc.good != "NULL" {
							good, _ = strconv.ParseInt(tc.good, 10, 64)
						}
						retained := [][]any{}
						for _, prepared := range []bool{false, true} {
							step := fixture.Exec("INSERT INTO "+name+" VALUES("+tc.good+",NULL)", 1)
							step.Prepared = prepared
							run(step)
							retained = append(retained, []any{good, nil})
							if tc.name != "null_count" && tc.name != "nonnull" && tc.name != "nonnull_single" {
								step = fixture.Exec("INSERT INTO "+name+" VALUES(NULL,NULL)", 1)
								step.Prepared = prepared
								run(step)
								retained = append([][]any{{nil, nil}}, retained...)
							}
							detail := tc.bad
							if detail == "NULL" {
								detail = "null"
							}
							for _, query := range []string{"INSERT INTO " + name + " VALUES(" + tc.bad + ",NULL)", "INSERT INTO " + name + " VALUES(" + tc.good + ",NULL),(" + tc.bad + ",NULL),(" + tc.good + ",NULL)", "UPDATE " + name + " SET a=" + tc.bad} {
								step = fixture.ImplicitError(query, fixture.CheckViolation(name, constraint, detail+", null"))
								step.Prepared = prepared
								run(step)
								run(fixture.Select("SELECT a,b FROM "+name+" ORDER BY a NULLS FIRST", []string{"a", "b"}, []string{"INT4", "INT4"}, retained))
							}
						}
						count := int64(4)
						if tc.name == "null_count" || tc.name == "nonnull" || tc.name == "nonnull_single" {
							count = 2
						}
						run(fixture.Select("SELECT COUNT(*) AS n FROM "+name, []string{"n"}, []string{"INT8"}, [][]any{{count}}))
					}
				})
			}
		})
	}
}
