package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"testing"
)

// Original public consumer routes run in fresh phases; the Context companions remain intact.
func TestReferenceDeparseConstraintsReviewNestedBehaviorSourceConsumerRoutes(t *testing.T) {
	for _, tc := range deparseReviewBehavior {
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
					run(referenceExec("CREATE TABLE original(a INT,b INT,CONSTRAINT c CHECK("+tc.input+"))", 0))
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
						run(referenceSelect(query, []string{"d"}, []string{"TEXT"}, [][]any{{want}}))
						run(referenceExec(fmt.Sprintf("CREATE TABLE copy%d(a INT,b INT,CONSTRAINT c%d %s)", i, i, suffix), 0))
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
							step := referenceExec("INSERT INTO "+name+" VALUES("+tc.good+",NULL)", 1)
							step.prepared = prepared
							run(step)
							retained = append(retained, []any{good, nil})
							if tc.name != "null_count" && tc.name != "nonnull" {
								step = referenceExec("INSERT INTO "+name+" VALUES(NULL,NULL)", 1)
								step.prepared = prepared
								run(step)
								retained = append([][]any{{nil, nil}}, retained...)
							}
							detail := tc.bad
							if detail == "NULL" {
								detail = "null"
							}
							for _, query := range []string{"INSERT INTO " + name + " VALUES(" + tc.bad + ",NULL)", "INSERT INTO " + name + " VALUES(" + tc.good + ",NULL),(" + tc.bad + ",NULL),(" + tc.good + ",NULL)", "UPDATE " + name + " SET a=" + tc.bad} {
								step = referenceImplicitError(query, referenceCheckViolation(name, constraint, detail+", null"))
								step.prepared = prepared
								run(step)
								run(referenceSelect("SELECT a,b FROM "+name+" ORDER BY a NULLS FIRST", []string{"a", "b"}, []string{"INT4", "INT4"}, retained))
							}
						}
						count := int64(4)
						if tc.name == "null_count" || tc.name == "nonnull" {
							count = 2
						}
						run(referenceSelect("SELECT COUNT(*) AS n FROM "+name, []string{"n"}, []string{"INT8"}, [][]any{{count}}))
					}
				})
			}
		})
	}
}
