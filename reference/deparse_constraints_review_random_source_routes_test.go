package reference_test

import (
	"database/sql"
	"fmt"
	"testing"
)

// The legacy smoke's plain QueryRow/Prepare/Exec routes complement the richer
// Context random-state and atomicity matrix without duplicating that matrix.
func TestReferenceDeparseConstraintsReviewRandomZeroSourceConsumerRoutes(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			db := b.db
			db.SetMaxOpenConns(1)
			run := func(s referenceStep) {
				s.nonContext = true
				if s.selectRows && s.diagnostic == nil {
					s.checkMetadata = func(t *testing.T, rows *sql.Rows) {
						deparseReviewMetadata(t, b.pg, rows)
						deparseReviewNullableColumns(t, b.pg, rows, "p")
					}
				}
				referenceRunStep(t, p.ctx, db, b.recorder, s)
			}
			seed := func() {
				var ignored any
				if err := db.QueryRow("SELECT setseed(0.25)").Scan(&ignored); err != nil {
					t.Fatal(err)
				}
			}
			draw := func(query string) float64 {
				var value float64
				if err := db.QueryRow(query).Scan(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			run(referenceExec("CREATE TABLE original(p DOUBLE PRECISION,CONSTRAINT c CHECK(random_normal(5,0)>p))", 0))
			for mode, want := range []string{
				"CHECK ((random_normal((5)::double precision, (0)::double precision) > p))",
				"CHECK (random_normal(5::double precision, 0::double precision) > p)",
			} {
				var actual string
				query := "SELECT pg_get_constraintdef(oid,$1) FROM pg_catalog.pg_constraint WHERE conname='c'"
				if err := db.QueryRow(query, mode == 1).Scan(&actual); err != nil || actual != want {
					t.Fatal(actual, want, err)
				}
				name := fmt.Sprintf("copy%d", mode)
				run(referenceExec("CREATE TABLE "+name+"(p DOUBLE PRECISION,CONSTRAINT copied "+actual+")", 0))
				for _, prepared := range []bool{false, true} {
					s := referenceExec("INSERT INTO "+name+" VALUES(4),(NULL)", 2)
					s.prepared = prepared
					run(s)
					s = referenceImplicitError("INSERT INTO "+name+" VALUES(5)", referenceCheckViolation(name, "copied", "5"))
					s.prepared = prepared
					run(s)
					seed()
					if got := draw("SELECT random_normal(5,0)"); got != 5 {
						t.Fatal("zero deviation", got)
					}
					control := draw("SELECT random()")
					seed()
					s = referenceExec("INSERT INTO "+name+" VALUES(4)", 1)
					s.prepared = prepared
					run(s)
					if got := draw("SELECT random()"); got != control {
						t.Fatal("CHECK consumed wrong random-state frontier", got, control)
					}
					run(referenceSelect("SELECT p FROM "+name+" ORDER BY p NULLS FIRST", []string{"p"}, []string{"FLOAT8"}, [][]any{{nil}, {float64(4)}, {float64(4)}}))
					run(referenceExec("DELETE FROM "+name, 3))
				}
			}
		})
	}
}
