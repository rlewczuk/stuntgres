package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func sqlJSONObjectAggregateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func sqlJSONObjectAggregateDuplicate(binary bool, key string) fixture.Diagnostic {
	message := "duplicate JSON object key value"
	if !binary {
		message += ": " + key
	}
	return fixture.ControlsReferenceDiagnostic("22030", message)
}
