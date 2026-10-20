package analyze

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// A held description bound to one catalog lifetime may be checked against a
// replacement owner or epoch: catalog-owned parameter and result types fail
// with the cached-plan error; builtin types and an unchanged lifetime pass.
func TestExpectedCheckLifetimeFencesCatalogTypes(t *testing.T) {
	mood, err := sqltype.Enum(90001, "mood", "mood", []string{"happy"})
	if err != nil {
		t.Fatal(err)
	}
	var int4 *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Int4 {
			int4 = d
		}
	}
	if int4 == nil {
		t.Fatal("missing builtin int4")
	}
	owner, replacement := &catalog.Owner{}, &catalog.Owner{}
	const epoch = ids.CatalogEpoch(4)
	parameter := Expected{Held: true, Parameters: ParameterInfo{Count: 1, Types: []sqltype.Usage{mood.Usage()}}, CatalogOwner: owner, CatalogEpoch: epoch}
	result := Expected{Held: true, Results: []catalog.Column{{Name: "m", Usage: mood.Usage()}}, CatalogOwner: owner, CatalogEpoch: epoch}
	builtin := Expected{Held: true, Parameters: ParameterInfo{Count: 1, Types: []sqltype.Usage{int4.Usage()}}, Results: []catalog.Column{{Name: "i", Usage: int4.Usage()}}, CatalogOwner: owner, CatalogEpoch: epoch}
	unbound := Expected{Held: true, Parameters: parameter.Parameters}

	cases := []struct {
		name     string
		expected Expected
		owner    *catalog.Owner
		epoch    ids.CatalogEpoch
		message  string
	}{
		{"same lifetime", parameter, owner, epoch, ""},
		{"replaced owner", parameter, replacement, epoch, "cached plan must not change parameter type"},
		{"replaced epoch", parameter, owner, epoch + 1, "cached plan must not change parameter type"},
		{"catalog result", result, replacement, epoch, "cached plan must not change result type"},
		{"builtin types", builtin, replacement, epoch + 1, ""},
		{"no held lifetime", unbound, replacement, epoch, ""},
	}
	for _, c := range cases {
		err := c.expected.CheckLifetime(c.owner, c.epoch)
		if c.message == "" {
			if err != nil {
				t.Errorf("%s: CheckLifetime = %v, want nil", c.name, err)
			}
			continue
		}
		var d *sqlstate.Error
		if !errors.As(err, &d) || d.Code != "0A000" || d.Message != c.message {
			t.Errorf("%s: CheckLifetime = %v, want 0A000 %q", c.name, err, c.message)
		}
	}
}
