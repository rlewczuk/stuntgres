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

	record := func(member *sqltype.Descriptor) *sqltype.Descriptor {
		t.Helper()
		shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "value", Usage: member.Usage()}})
		if err != nil {
			t.Fatal(err)
		}
		return shape
	}
	array := func(element *sqltype.Descriptor) *sqltype.Descriptor {
		t.Helper()
		shape, err := sqltype.Array(2287, "_record", element)
		if err != nil {
			t.Fatal(err)
		}
		return shape
	}
	domain, err := sqltype.Domain(90002, "count_domain", "public.count_domain", int4.Usage())
	if err != nil {
		t.Fatal(err)
	}
	named, err := sqltype.Composite(90003, "count_row", "public.count_row", 90004,
		[]sqltype.CompositeMember{{Attribute: 1, Name: "count", Usage: int4.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	builtinRecord := record(int4)
	for _, shape := range []struct {
		name    string
		typ     *sqltype.Descriptor
		catalog bool
	}{
		{"builtin record", builtinRecord, false},
		{"nested builtin record", record(builtinRecord), false},
		{"builtin record array", array(builtinRecord), false},
		{"named builtin-member composite", named, true},
		{"record enum member", record(mood), true},
		{"record domain member", record(domain), true},
		{"nested record enum member", record(record(mood)), true},
		{"record named composite member", record(named), true},
		{"catalog record array", array(record(mood)), true},
		{"record catalog-array member", record(array(record(domain))), true},
	} {
		if got := StatementCatalogType(shape.typ); got != shape.catalog {
			t.Fatal("record occurrence versus member lifetime", shape.name, got)
		}
		for _, result := range []bool{false, true} {
			expected := Expected{Held: true, CatalogOwner: owner, CatalogEpoch: epoch}
			message := "cached plan must not change parameter type"
			if result {
				expected.Results = []catalog.Column{{Name: "record", Usage: shape.typ.Usage()}}
				message = "cached plan must not change result type"
			} else {
				expected.Parameters = ParameterInfo{Count: 1, Types: []sqltype.Usage{shape.typ.Usage()}}
			}
			for _, changedOwner := range []bool{false, true} {
				nextOwner, nextEpoch := owner, epoch+1
				if changedOwner {
					nextOwner, nextEpoch = replacement, epoch
				}
				err := expected.CheckLifetime(nextOwner, nextEpoch)
				if !shape.catalog {
					if err != nil {
						t.Fatal("builtin-only anonymous shape acquired catalog lifetime", shape.name, result, err)
					}
					continue
				}
				var diagnostic *sqlstate.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != message {
					t.Fatal("nested catalog member escaped lifetime fence", shape.name, result, err)
				}
			}
		}
	}
}
