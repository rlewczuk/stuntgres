package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConditionalInternalCharEquality(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(step fixture.Step) {
				if step.SelectRows {
					step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						columns, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						for _, column := range columns {
							if column.DatabaseTypeName() != "CHAR" {
								continue
							}
							want := reflect.TypeFor[string]()
							if backend.PG {
								// Observed pinned pq metadata; raw CHAR values remain strings.
								want = reflect.TypeFor[any]()
							}
							if column.ScanType() != want {
								t.Fatalf("CHAR ScanType=%v want %v", column.ScanType(), want)
							}
						}
					}
				}
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, step)
			}
			run(fixture.Exec(`CREATE TABLE conditional_char_table(id INT)`, 0))
			run(fixture.Exec(`CREATE VIEW conditional_char_view AS SELECT id FROM conditional_char_table`, 0))
			// Catalog-owned internal CHAR values retain their descriptor in conditionals.
			step := fixture.Select(`SELECT relname AS name,CASE relkind WHEN 'r' THEN 'table' WHEN 'r' THEN 'late' ELSE 'other' END AS kind,CASE relkind WHEN 'r' THEN 'table' END AS omitted,CASE relkind WHEN NULL THEN 1 ELSE 0 END AS null_match,CASE relkind WHEN relkind THEN 1 ELSE 0 END AS typed,NULLIF(relkind,'r') AS unequal,CASE (CASE relkind WHEN 'r' THEN relkind END) WHEN 'r' THEN 1 ELSE 0 END AS nullable FROM pg_catalog.pg_class WHERE relname='conditional_char_table' OR relname='conditional_char_view' ORDER BY relname`, []string{"name", "kind", "omitted", "null_match", "typed", "unequal", "nullable"}, []string{"NAME", "TEXT", "TEXT", "INT4", "INT4", "CHAR", "INT4"}, [][]any{{"conditional_char_table", "table", "table", int64(0), int64(1), nil, int64(1)}, {"conditional_char_view", "other", nil, int64(0), int64(1), "v", int64(0)}})
			run(step)
			run(fixture.Prepared(step))
		})
	}
}
