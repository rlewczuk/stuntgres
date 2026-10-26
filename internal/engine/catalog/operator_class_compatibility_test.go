package catalog

import (
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestOperatorClassDefaultCompatibility(t *testing.T) {
	base := CloneState(familyCatalogFixture().State())
	types := map[sqltype.ID]*sqltype.Descriptor{}
	for _, typ := range sqltype.Builtins() {
		types[typ.ID()] = typ
		d := &Type{SQLType: typ, ObjectRevision: 1}
		base.SetDescriptor(d.Address(), d)
	}
	enum, err := sqltype.Enum(96000, "compatibility_enum", "public.compatibility_enum", []string{"low", "high"})
	if err != nil {
		t.Fatal(err)
	}
	domain, err := sqltype.Domain(96001, "compatibility_domain", "public.compatibility_domain", types[sqltype.VarChar].Usage())
	if err != nil {
		t.Fatal(err)
	}
	enumDomain, err := sqltype.Domain(96002, "compatibility_enum_domain", "public.compatibility_enum_domain", enum.Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range []*sqltype.Descriptor{enum, domain, enumDomain} {
		d := &Type{SQLType: typ, ObjectRevision: 1}
		base.SetDescriptor(d.Address(), d)
	}
	for _, tc := range []struct {
		name     string
		typ      *sqltype.Descriptor
		fallback ids.OID
		gist     bool
	}{
		{"varchar", types[sqltype.VarChar], 25, false},
		{"domain_varchar", domain, 25, false},
		{"enum", enum, 3500, false},
		{"domain_enum", enumDomain, 3500, false},
		{"range", types[3904], 3831, true},
		{"multirange", types[4451], 4537, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := CloneState(base)
			ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 11}
			method := &AccessMethod{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 97000}, ObjectName: "compatibility_method", ObjectRevision: 1, Native: BtreeAccessMethod, Handler: 330}
			if tc.gist {
				method.Native, method.Handler = GistAccessMethod, 332
			}
			family := &OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 97001}, ObjectNamespace: ns, ObjectName: "compatibility_family", ObjectRevision: 1, Method: 97000}
			for _, d := range []Descriptor{method, family} {
				state.SetDescriptor(d.Address(), d)
				state.BindName(DescriptorNameKey(d), d.Address())
			}
			add := func(oid, input ids.OID, name string) *OperatorClass {
				class := &OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: oid}, ObjectNamespace: ns, ObjectName: name, ObjectRevision: 1, Method: 97000, Family: 97001, InputType: input, Default: true}
				state.SetDescriptor(class.Address(), class)
				state.BindName(DescriptorNameKey(class), class.Address())
				return class
			}
			check := func(read Read, want ids.OID, ambiguous bool) {
				t.Helper()
				class, ok, err := read.DefaultIndexClass(97000, tc.typ)
				if ambiguous {
					var ambiguity *DefaultClassAmbiguity
					var diagnostic *sqlstate.Error
					if ok || class.OID() != 0 || !errors.As(err, &ambiguity) || ambiguity.Input != tc.typ.BaseType().ID() || !errors.As(err, &diagnostic) || diagnostic.Code != "42710" || diagnostic.Detail != "" || diagnostic.Hint != "" {
						t.Fatal("exact ambiguity identity/diagnostic", class, ok, err)
					}
				} else if err != nil || ok != (want != 0) || class.OID() != uint32(want) {
					t.Fatal("typed selection", class, ok, err, "want", want)
				}
				optional, found := read.UniqueDefaultIndexClass(97000, tc.typ)
				if found != (want != 0) || optional.OID() != uint32(want) {
					t.Fatal("optional selection diverged", optional, found, "want", want)
				}
				// An ID alone does not convey a concrete enum descriptor. Other
				// ID routes use the same base and already shipped adaptation.
				idWant := want
				if tc.typ.BaseType().IsEnum() && want == 97002 {
					idWant = 0
				}
				byID, idOK, idErr := read.DefaultIndexClassID(97000, tc.typ.BaseType().ID())
				if ambiguous {
					var ambiguity *DefaultClassAmbiguity
					if idOK || byID.OID() != 0 || !errors.As(idErr, &ambiguity) || ambiguity.Input != tc.typ.BaseType().ID() || ambiguity.SQLState() != "42710" {
						t.Fatal("ID exact ambiguity identity", byID, idOK, idErr)
					}
				} else if idErr != nil || idOK != (idWant != 0) || byID.OID() != uint32(idWant) {
					t.Fatal("ID selection", byID, idOK, idErr, "want", idWant)
				}
			}
			empty := NewRead(nil, CloneState(state))
			check(empty, 0, false)
			first := add(97002, tc.fallback, "first_compatible")
			one := NewRead(nil, CloneState(state))
			check(one, 97002, false)
			add(97003, tc.fallback, "second_compatible")
			multiple := NewRead(nil, CloneState(state))
			check(multiple, 0, false)
			if _, ok := multiple.DefaultOperatorClass(97000, tc.fallback); ok {
				t.Fatal("exact metadata picked a compatible bucket winner")
			}
			if tc.gist {
				for _, read := range []Read{one, multiple} {
					for _, input := range []*sqltype.Descriptor{types[sqltype.Int4], types[sqltype.Text]} {
						class, ok, err := read.DefaultIndexClass(97000, input)
						if ok || err != nil || class.OID() != 0 {
							t.Fatal("unrelated GiST bucket poisoned input", input.ID(), class, ok, err)
						}
						if _, ok := read.UniqueDefaultIndexClass(97000, input); ok {
							t.Fatal("unrelated optional GiST selection")
						}
					}
				}
			}
			// Exact cardinality precedes the compatible bucket, even when that
			// bucket has duplicates. Domains use their base input identity.
			add(97004, ids.OID(tc.typ.BaseType().ID()), "first_exact")
			check(NewRead(nil, state), 97004, false)
			add(97005, ids.OID(tc.typ.BaseType().ID()), "second_exact")
			check(NewRead(nil, state), 0, true)
			state.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: 97003})
			check(NewRead(nil, state), 0, true)
			// Strict selection never changes named provider construction or old
			// immutable reads, and never inherits the builtin method's default.
			if class, ok := multiple.IndexClassForMethod(first.Address().ObjectID, method); !ok || class.OID() != 97002 {
				t.Fatal("explicit provider lost to multiplicity", class, ok)
			}
			check(one, 97002, false)
			check(empty, 0, false)
			if _, ok, err := empty.DefaultIndexClass(97000, types[sqltype.Int4]); ok || err != nil {
				t.Fatal("SQL alias inherited builtin defaults", ok, err)
			}
			if _, ok, err := multiple.DefaultIndexClass(97000, nil); ok || err != nil {
				t.Fatal("nil type selected a default", ok, err)
			}
		})
	}
}
