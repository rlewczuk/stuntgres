package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// The legacy smoke's plain QueryRow/Prepare/Exec routes complement the richer
// Context random-state and atomicity matrix without duplicating that matrix.
func TestReferenceDeparseConstraintsReviewRandomZeroSourceConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			db := b.DB
			db.SetMaxOpenConns(1)
			run := func(s fixture.Step) {
				s.NonContext = true
				if s.SelectRows && s.Diagnostic == nil {
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						fixture.DeparseReviewMetadata(t, b.PG, rows)
						deparseReviewNullableColumns(t, b.PG, rows, "p")
					}
				}
				fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
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
			run(fixture.Exec("CREATE TABLE original(p DOUBLE PRECISION,CONSTRAINT c CHECK(random_normal(5,0)>p))", 0))
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
				run(fixture.Exec("CREATE TABLE "+name+"(p DOUBLE PRECISION,CONSTRAINT copied "+actual+")", 0))
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec("INSERT INTO "+name+" VALUES(4),(NULL)", 2)
					s.Prepared = prepared
					run(s)
					s = fixture.ImplicitError("INSERT INTO "+name+" VALUES(5)", fixture.CheckViolation(name, "copied", "5"))
					s.Prepared = prepared
					run(s)
					seed()
					if got := draw("SELECT random_normal(5,0)"); got != 5 {
						t.Fatal("zero deviation", got)
					}
					control := draw("SELECT random()")
					seed()
					s = fixture.Exec("INSERT INTO "+name+" VALUES(4)", 1)
					s.Prepared = prepared
					run(s)
					if got := draw("SELECT random()"); got != control {
						t.Fatal("CHECK consumed wrong random-state frontier", got, control)
					}
					run(fixture.Select("SELECT p FROM "+name+" ORDER BY p NULLS FIRST", []string{"p"}, []string{"FLOAT8"}, [][]any{{nil}, {float64(4)}, {float64(4)}}))
					run(fixture.Exec("DELETE FROM "+name, 3))
				}
			}
		})
	}
}
