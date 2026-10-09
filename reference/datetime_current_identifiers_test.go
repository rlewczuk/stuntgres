package reference_test

import (
	"strings"
	"testing"
)

// Quoted current-family identifiers must stay ordinary table/column names even
// immediately before a declaration or INSERT column-list opener. The expected
// integers are independent of clocks and of the SQL value functions.
func TestReferenceDatetimeCurrentQuotedIdentifiers(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			conn, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			for _, spelling := range datetimeCurrentNames {
				for _, name := range []string{spelling, strings.ToUpper(spelling)} {
					t.Run(name, func(t *testing.T) {
						quoted := `"` + name + `"`
						datetimeCurrentExec(t, p.ctx, conn, "CREATE TABLE "+quoted+" ("+quoted+" INTEGER)", 0)
						datetimeCurrentExec(t, p.ctx, conn, "INSERT INTO "+quoted+" ("+quoted+") VALUES(7)", 1)
						if name == spelling {
							// An unquoted keyword after a schema qualifier is also a
							// relation label, including before the INSERT list opener.
							datetimeCurrentExec(t, p.ctx, conn, "INSERT INTO public."+spelling+" ("+quoted+") VALUES(8)", 1)
						} else {
							datetimeCurrentExec(t, p.ctx, conn, "INSERT INTO public."+quoted+" ("+quoted+") VALUES(8)", 1)
						}
						q := "SELECT " + quoted + " FROM public." + quoted + " ORDER BY " + quoted
						step := referenceSelect(q, []string{name}, []string{"INT4"}, [][]any{{int64(7)}, {int64(8)}})
						referenceRunStep(t, p.ctx, conn, backend.recorder, step)
						referenceRunStep(t, p.ctx, conn, backend.recorder, referencePrepared(step))
					})
				}
			}
		})
	}
}
