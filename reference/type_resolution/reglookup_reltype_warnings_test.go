package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRegLookupRelTypeWarnings(t *testing.T) {
	regLookupScenario(t, nil, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
		var notices []fixture.Diagnostic
		_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		b.Recorder = recorder
		want := fixture.Diagnostic{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "22023", Message: "TIMESTAMP(7) precision reduced to maximum allowed, 6"}
		for _, tc := range []struct {
			name, kind string
			output     any
		}{
			{"to_regtype", "REGTYPE", "timestamp without time zone"},
			{"to_regtypemod", "INT4", int64(6)},
		} {
			for _, formal := range []string{"'timestamp(7)'", "$1", "$1::text"} {
				t.Run(tc.name+"/"+formal, func(t *testing.T) {
					q := "SELECT " + tc.name + "(" + formal + ") AS value"
					notices = nil
					held := regLookupHeld(t, p, c, q)
					defer held.Statement.Close()
					if len(notices) != 0 {
						t.Fatalf("Prepare emitted warnings: %+v", notices)
					}
					var args []any
					if formal != "'timestamp(7)'" {
						args = []any{"timestamp(7)"}
					}
					for _, route := range []string{"query", "fresh-prepare", "held-first", "held-again"} {
						notices = nil
						s := regLookupRelTypeSelect(b, q, []string{"value"}, []string{tc.kind}, [][]any{{tc.output}}, args...)
						switch route {
						case "query":
							regObjectRunStep(t, p, b, c, s)
						case "fresh-prepare":
							regObjectRunStep(t, p, b, c, fixture.Prepared(s))
						default:
							regObjectRunStep(t, p, b, held, s)
						}
						if !reflect.DeepEqual(notices, []fixture.Diagnostic{want}) {
							t.Errorf("%s notices=%+v want %+v", route, notices, want)
						}
					}
					if len(args) != 0 {
						notices = nil
						regObjectRunStep(t, p, b, held, regLookupRelTypeSelect(b, q, []string{"value"}, []string{tc.kind}, [][]any{{nil}}, nil))
						if len(notices) != 0 {
							t.Errorf("strict NULL emitted destination warning: %+v", notices)
						}
					}
				})
			}
		}
	})
}
