package reference_test

import (
	"stuntgres/reference/fixture"
)

func regtypeInputDiagnostic(fact regtypeInvalidInput, route string) fixture.Diagnostic {
	want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: fact.state, Message: fact.primary}
	if fact.parseContext {
		want.Context = `invalid type name "` + fact.input + `"`
	}
	switch route {
	case "literal-query", "literal-prepare":
		want.Position = "17"
	case "direct-frame", "held-frame":
		want.Position = fact.position
		if want.Context != "" {
			want.Context += "\n"
		}
		text := "..."
		if fact.input == "" {
			text = ""
		}
		want.Context += "unnamed portal parameter $1 = '" + text + "'"
	case "runtime-text", "held-runtime-text":
		want.Position = fact.position
	default:
		panic("unknown regtype input route: " + route)
	}
	return want
}
