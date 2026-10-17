package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// The catalog portion of the external NAME ABI witness is now shared; binary
// parameters/namesend external-server controls remain in name_external_test.go.
func TestReferenceNameCatalogFacts(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			s := fixture.Select("SELECT oid::bigint,typlen::bigint,typbyval,typalign,typcategory,typinput,typoutput,typreceive,typsend,typcollation::bigint,typarray::bigint FROM pg_type WHERE typname='name'", []string{"oid", "typlen", "typbyval", "typalign", "typcategory", "typinput", "typoutput", "typreceive", "typsend", "typcollation", "typarray"}, []string{"INT8", "INT8", "BOOL", "CHAR", "CHAR", "REGPROC", "REGPROC", "REGPROC", "REGPROC", "INT8", "INT8"}, [][]any{{int64(19), int64(64), false, "c", "S", "namein", "nameout", "namerecv", "namesend", int64(950), int64(1003)}})
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Prepared(s))
		})
	}
}
