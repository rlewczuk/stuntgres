package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func citextPatternScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, backend, c)
		})
	}
}

var citextPatternOps = []struct {
	sql, catalog, routine, negator string
	negative, regex                bool
}{
	{"~", "~", "texticregexeq", "!~", false, true},
	{"~*", "~*", "texticregexeq", "!~*", false, true},
	{"!~", "!~", "texticregexne", "~", true, true},
	{"!~*", "!~*", "texticregexne", "~*", true, true},
	{"LIKE", "~~", "texticlike", "!~~", false, false},
	{"ILIKE", "~~*", "texticlike", "!~~*", false, false},
	{"NOT LIKE", "!~~", "texticnlike", "~~", true, false},
	{"NOT ILIKE", "!~~*", "texticnlike", "~~*", true, false},
}

func citextPatternBool(q string, value any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{value}}, args...)
}

func citextPatternRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	citextComparisonRoutes(t, p, b, c, s)
}

func citextPatternProjection(right string) (string, []string, []string) {
	var expressions, names, types []string
	for _, rhs := range []string{"p", right} {
		for _, op := range citextPatternOps {
			name := "c" + string(rune('a'+len(names)))
			expressions = append(expressions, "s "+op.sql+" "+rhs+" AS "+name)
			names = append(names, name)
			types = append(types, "BOOL")
		}
	}
	return strings.Join(expressions, ","), names, types
}

func citextPatternRows() [][]any {
	return [][]any{
		{int64(1), true, true, false, false, true, true, false, false, true, true, false, false, true, true, false, false, false, true},
		{int64(2), false, false, true, true, false, false, true, true, false, false, true, true, false, false, true, true, false, true},
		{int64(3), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, true},
	}
}

func citextPatternRegexDiagnostic(message string) fixture.Diagnostic {
	return fixture.ControlsReferenceDiagnostic("2201B", "invalid regular expression: "+message)
}
