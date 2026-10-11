package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceINETScalarDomainAndArrayBoundary(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE DOMAIN network_host AS INET", 0))
	p.Step(t, fixture.Exec("CREATE TABLE domain_n(h network_host)", 0))
	p.Step(t, fixture.Exec("INSERT INTO domain_n VALUES('192.168.0.129/25'),(NULL)", 2))
	p.Step(t, fixture.Select("SELECT h::inet AS h,h::cidr AS c FROM domain_n ORDER BY h", []string{"h", "c"}, []string{"INET", "CIDR"}, [][]any{{"192.168.0.129/25", "192.168.0.128/25"}, {nil, nil}}))
	// PostgreSQL implements these arrays. Native's scalar-only boundary is an
	// independent capability assertion, never an invented PostgreSQL rejection.
	for _, b := range p.Backends() {
		if !b.PG {
			for _, q := range []string{"CREATE TABLE excluded(h INET[])", "CREATE TABLE excluded(h CIDR[])", "CREATE TABLE excluded(h network_host[])"} {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "unsupported column type", Position: "25"}))
			}
		}
	}
}
