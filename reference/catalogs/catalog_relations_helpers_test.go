package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func catalogRelationsBackends(t *testing.T, p *fixture.Pair, body func(*testing.T, fixture.Backend)) {
	t.Helper()
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, backend) })
	}
}
