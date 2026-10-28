package catalog

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

func TestTSParserPrincipalRemap(t *testing.T) {
	sourceDomain, destinationDomain := &authorization.Domain{}, &authorization.Domain{}
	state := NewState(1)
	InstallBuiltinTSParsers(state)
	seedAddress := ids.ObjectAddress{ClassID: ids.TSParserClassID, ObjectID: 3722}
	seed := state.MustTSParser(seedAddress)
	modifiedSeed := *seed
	modifiedSeed.ObjectName = "relocated_default"
	modifiedSeed.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	modifiedSeed.ObjectRevision = 2
	state.UnbindName(DescriptorNameKey(seed))
	state.SetDescriptor(modifiedSeed.Address(), &modifiedSeed)
	state.BindName(DescriptorNameKey(&modifiedSeed), modifiedSeed.Address())
	custom := modifiedSeed
	custom.ObjectAddress.ObjectID = 90001
	custom.ObjectName = "principal_remap_parser"
	custom.ObjectRevision = 3
	custom.Headline = 0
	state.SetDescriptor(custom.Address(), &custom)
	state.BindName(DescriptorNameKey(&custom), custom.Address())
	configuration := &TSConfiguration{
		ObjectAddress:   ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 90002},
		ObjectNamespace: custom.Namespace(), ObjectName: "principal_remap_config", ObjectRevision: 4,
		Owner: sourceDomain.Principal(20), Parser: custom.Address(),
		Mappings: []TSMapping{{TokenID: 1, Dictionaries: []ids.ObjectAddress{{ClassID: ids.TSDictionaryClassID, ObjectID: 3765}}}},
	}
	state.SetDescriptor(configuration.Address(), configuration)
	state.BindName(DescriptorNameKey(configuration), configuration.Address())
	next := CloneState(state)
	mappedOwners := 0
	if err := RemapPrincipals(next, PrincipalRemap{
		Principal: func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
			t.Fatal("ownerless parser acquired an ordinary principal", ref)
			return authorization.PrincipalRef{}, nil
		},
		SeededOwner: func(ref authorization.PrincipalRef, seeded bool) (authorization.PrincipalRef, error) {
			if seeded || ref != configuration.Owner {
				t.Fatal("unexpected parser/configuration principal mapping", ref, seeded)
			}
			mappedOwners++
			return destinationDomain.Principal(20), nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	if mappedOwners != 1 {
		t.Fatal("configuration owner was not remapped", mappedOwners)
	}
	for _, parser := range []*TSParser{&modifiedSeed, &custom} {
		if got := next.MustTSParser(parser.Address()); got != parser || *got != *parser {
			t.Fatal("principal remap changed parser identity, revision or callbacks", got, parser)
		}
		if next.NameAddress(DescriptorNameKey(parser)) != parser.Address() || state.Get(parser.Address()) != parser {
			t.Fatal("principal remap changed parser name or original root")
		}
	}
	mapped := next.MustTSConfiguration(configuration.Address())
	if mapped == configuration || mapped.Owner != destinationDomain.Principal(20) || configuration.Owner != sourceDomain.Principal(20) || mapped.Parser != custom.Address() {
		t.Fatal("configuration remap lost owner isolation or actual parser reference", mapped, configuration)
	}
	if &mapped.Mappings[0] != &configuration.Mappings[0] || &mapped.Mappings[0].Dictionaries[0] != &configuration.Mappings[0].Dictionaries[0] {
		t.Fatal("principal-only remap rebuilt immutable configuration mapping backing")
	}
	if seed.Name() != "default" || seed.Namespace().ObjectID != ids.PGCatalogNamespaceOID || seed.Headline != 3720 || custom.Headline != 0 {
		t.Fatal("principal remap mutated original seed or optional callback authority")
	}
}
