package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func internalCharMetadata(t *testing.T, rows *sql.Rows, pg bool, nullable []bool) []*sql.ColumnType {
	t.Helper()
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range columns {
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if c.DatabaseTypeName() != "CHAR" || c.ScanType() != scan {
			t.Fatalf("%s type/scan: %s/%v", c.Name(), c.DatabaseTypeName(), c.ScanType())
		}
		if n, known := c.Length(); n != 0 || known {
			t.Fatalf("%s length: %d/%v", c.Name(), n, known)
		}
		if p, s, known := c.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatalf("%s precision: %d/%d/%v", c.Name(), p, s, known)
		}
		n, known := c.Nullable()
		if pg || nullable == nil {
			if n || known {
				t.Fatalf("%s nullability: %v/%v, want unknown", c.Name(), n, known)
			}
		} else if !known || n != nullable[i] {
			t.Fatalf("%s nullability: %v/%v, want %v/true", c.Name(), n, known, nullable[i])
		}
	}
	return columns
}

func TestReferenceInternalCharMetadata(t *testing.T) {
	internalCharScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE internal_char_metadata(id integer,v "char" NOT NULL,n pg_catalog."char")`, 0))
		run(fixture.Exec(`INSERT INTO internal_char_metadata VALUES(1,'r',NULL)`, 1))
		for _, prepared := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				for _, computed := range []bool{false, true} {
					q := `SELECT v,n FROM internal_char_metadata`
					if computed {
						q = `SELECT 'r'::pg_catalog."char" AS v,NULL::"char" AS n`
					}
					want := [][]any{{"r", nil}}
					if empty {
						q += ` WHERE FALSE`
						want = nil
					}
					s := fixture.Select(q, []string{"v", "n"}, []string{"CHAR", "CHAR"}, want)
					s.Prepared = prepared
					var retained []*sql.ColumnType
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						nullable := []bool{false, true}
						if computed {
							nullable = nil
						}
						retained = internalCharMetadata(t, rows, b.PG, nullable)
					}
					s.CheckScan = func(t *testing.T, rows *sql.Rows, _ int) {
						var v, n any
						if err := rows.Scan(&v, &n); err != nil {
							t.Fatal(err)
						}
						if value, ok := v.(string); !ok || value != "r" || n != nil {
							t.Fatalf("raw CHAR metadata witness: %T %v / %T %v", v, v, n, n)
						}
					}
					run(s)
					fixture.ScalarAssertDescription(t, retained, s.Columns, s.Types)
				}
			}
		}
	})
}
