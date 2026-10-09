package reference_test

import (
	"strconv"
	"strings"
	"testing"
)

// Preserve the former public visibility boundary witness in the shared fixture.
// Domains now have their own positive and diagnostic acceptance scenarios.
func TestReferenceVisibilityInquiryUtilityBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			run := func(s referenceStep) {
				t.Helper()
				referenceRunStep(t, p.ctx, conn, backend.recorder, s)
			}
			run(referenceExec("CREATE TABLE sentinel(id INT)", 0))
			run(referenceExec("INSERT INTO sentinel VALUES(7)", 1))
			for _, q := range []string{
				"CREATE TYPE widget AS ENUM ('x')",
				"CREATE COLLATION c FROM pg_catalog.\"C\"",
				"CREATE CONVERSION c FOR 'UTF8' TO 'LATIN1' FROM pg_catalog.utf8_to_iso8859_1",
				"CREATE TEMP TABLE temp_t(id INT)",
				"CREATE MATERIALIZED VIEW mv AS SELECT id FROM sentinel",
				"CREATE FOREIGN TABLE ft(id integer) SERVER unavailable",
			} {
				if backend.pg {
					switch {
					case strings.HasPrefix(q, "CREATE FOREIGN"):
						want := controlsReferenceDiagnostic("42704", `server "unavailable" does not exist`)
						run(referenceErrorDiagnostic(q, want))
					case strings.HasPrefix(q, "CREATE MATERIALIZED"):
						run(referenceExec(q, 1))
						run(referenceSelect("SELECT id FROM mv", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					default:
						run(referenceExec(q, 0))
					}
				} else {
					token := strings.Fields(q)[1]
					want := controlsReferenceDiagnostic("42601", `syntax error at or near "`+token+`"`)
					want.Position = "8"
					if token == "TEMP" {
						want.Code = "0A000"
						want.Message = "CREATE syntax is unsupported"
					}
					s := referenceErrorDiagnostic(q, want)
					s.selectRows = true // Utility analysis must not expose a cursor.
					run(s)
				}
				run(referenceSelect("SELECT pg_collation_is_visible(950),pg_conversion_is_visible(4519)", []string{"pg_collation_is_visible", "pg_conversion_is_visible"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
				run(referenceSelect("SELECT id FROM sentinel", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
			}
			const defaultSQL = "CREATE TABLE default_boundary(id BOOL DEFAULT pg_type_is_visible(23))"
			if backend.pg {
				run(referenceExec(defaultSQL, 0))
				run(referenceExec("INSERT INTO default_boundary DEFAULT VALUES", 1))
				run(referenceSelect("SELECT id FROM default_boundary", []string{"id"}, []string{"BOOL"}, [][]any{{true}}))
			} else {
				want := controlsReferenceDiagnostic("0A000", "functions are unsupported")
				want.Position = strconv.Itoa(strings.Index(defaultSQL, "pg_type_is_visible") + 1)
				s := referenceErrorDiagnostic(defaultSQL, want)
				s.selectRows = true
				run(s)
			}
		})
	}
}
