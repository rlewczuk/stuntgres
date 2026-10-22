package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceViewReplaceCapabilityBoundariesConsumerRoutes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		db := backend.DB
		for _, q := range []string{"CREATE TABLE t(id INT)", "INSERT INTO t VALUES(7)", "CREATE VIEW v AS SELECT id FROM t"} {
			fixture.UpdateReturningReferenceExec(t, db, q)
		}
		for _, q := range []string{"CREATE VIEW excluded WITH(check_option=local) AS SELECT id FROM t", "CREATE OR REPLACE VIEW v WITH(check_option=local) AS SELECT id FROM t", "ALTER VIEW v SET(check_option=local)"} {
			r, err := db.Exec(q)
			if err != nil {
				t.Fatal(q, err)
			}
			fixture.DeleteReferenceCount(t, r, err, 0)
		}
		for _, q := range []string{"ALTER VIEW v ALTER COLUMN id SET DEFAULT 7", "ALTER VIEW v ALTER COLUMN id DROP DEFAULT"} {
			r, err := db.Exec(q)
			if backend.PG {
				if err != nil {
					t.Fatal(q, err)
				}
				fixture.DeleteReferenceCount(t, r, err, 0)
			} else {
				fixture.UpdateReturningReferenceDiagnostic(t, err, fixture.Diagnostic{Code: "0A000", Message: "ALTER VIEW column defaults are unsupported", Position: "14"})
			}
		}
		fixture.UpdateReturningReferenceExec(t, db, "ALTER VIEW v RESET(unknown_option,unknown_option,check_option)")
		rows, err := db.Query("SELECT id FROM v")
		fixture.DeleteReferenceConsume(t, backend.PG, rows, err, fixture.Step{Columns: []string{"id"}, Types: []string{"INT4"}, Rows: [][]any{{int64(7)}}, Bag: false})
	}
}
