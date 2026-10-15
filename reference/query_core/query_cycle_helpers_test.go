package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

const cycleGraph = "WITH RECURSIVE r(n) AS (SELECT 'a'::text UNION ALL SELECT e.dst FROM r JOIN cycle_edges e ON e.src=r.n) CYCLE n SET cyc USING path "
const cycleLate = "WITH RECURSIVE r(n,v) AS (SELECT 1,1 UNION ALL SELECT n+1,1/(3-n) FROM r WHERE n<4) CYCLE n SET cyc USING path "
const cycleFinite = "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) CYCLE n SET cyc USING path "

func cycleSeed(run func(fixture.Step)) {
	run(fixture.Exec("CREATE TABLE cycle_edges(src TEXT,dst TEXT)", 0))
	run(fixture.Exec("INSERT INTO cycle_edges VALUES('a','b'),('b','a'),('a','c')", 3))
}

func cycleGraphRows() [][]any {
	return [][]any{{"a", false, "{(a)}"}, {"b", false, "{(a),(b)}"}, {"c", false, "{(a),(c)}"}, {"a", true, "{(a),(b),(a)}"}}
}

func cycleGraphStep(q string) fixture.Step {
	return cteBag(q, []string{"n", "cyc", "path"}, []string{"TEXT", "BOOL", "_RECORD"}, cycleGraphRows())
}

func cycleConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func cyclePosition(q, marker string) string {
	i := strings.Index(q, marker)
	if i < 0 {
		panic("missing diagnostic marker: " + marker)
	}
	return fixture.Position(utf8.RuneCountInString(q[:i]) + 1)
}

func cycleDiagnostic(code, message, q, marker string) fixture.Diagnostic {
	d := fixture.ControlsReferenceDiagnostic(code, message)
	if marker != "" {
		d.Position = cyclePosition(q, marker)
	}
	return d
}

// Assert complete diagnostics and failure publication on every public route.
func cycleAssertDiagnostic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want fixture.Diagnostic, executionOnly ...bool) {
	t.Helper()
	for _, route := range []string{"query", "exec", "prepare"} {
		t.Run(route, func(t *testing.T) {
			var err error
			phase := "execute"
			switch route {
			case "query":
				var rows *sql.Rows
				rows, err = c.QueryContext(p.Ctx, q)
				if rows != nil {
					rows.Close()
					t.Error("expected analysis failure published Rows")
				}
			case "exec":
				var result sql.Result
				result, err = c.ExecContext(p.Ctx, q)
				if result != nil {
					t.Error("failed Exec published result")
				}
			case "prepare":
				phase = "prepare"
				var stmt *sql.Stmt
				stmt, err = c.PrepareContext(p.Ctx, q)
				if stmt != nil {
					if err != nil {
						t.Error("failed Prepare published statement")
					}
					defer stmt.Close()
					if err == nil {
						phase = "execute held"
						var result sql.Result
						result, err = stmt.ExecContext(p.Ctx)
						if result != nil {
							t.Error("failed held Exec published result")
						}
					}
				}
			}
			if err == nil {
				t.Error("expected analysis failure")
				return
			}
			got := fixture.ExtractDiagnostic(t, err)
			if !reflect.DeepEqual(got, want) {
				t.Errorf("diagnostic got=%+v independently expected=%+v", got, want)
			}
			wantPhase := "execute"
			if route == "prepare" {
				wantPhase = "prepare"
				if len(executionOnly) > 0 && executionOnly[0] {
					wantPhase = "execute held"
				}
			}
			if phase != wantPhase {
				t.Errorf("backend=%s route=%s phase=%s want=%s diagnostic=%+v", b.Name, route, phase, wantPhase, got)
			}
		})
	}
}
