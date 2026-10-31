package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func TestTSConfigurationProvider(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	seed := view.MustTSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748})
	provider := catalog.TSConfigurationProvider{}
	if err := provider.Validate(view, seed); err != nil {
		t.Fatal(err)
	}
	if len(seed.Mappings) != 19 || seed.Mapping(12) != nil || seed.Mapping(23) != nil {
		t.Fatal("actual stock mappings", seed.Mappings)
	}
	for _, change := range []func(*catalog.TSConfiguration){func(d *catalog.TSConfiguration) { d.Owner = (&authorization.Domain{}).Principal(d.Owner.ID()) }, func(d *catalog.TSConfiguration) { d.Parser.ObjectID++ }, func(d *catalog.TSConfiguration) { d.Mappings[0].TokenID = 0 }, func(d *catalog.TSConfiguration) { d.Mappings[0].Dictionaries = nil }, func(d *catalog.TSConfiguration) { d.Mappings[0].Dictionaries[0].ObjectID++ }, func(d *catalog.TSConfiguration) { d.Mappings = append(d.Mappings, d.Mappings[0]) }} {
		next := *seed
		next.Mappings = catalog.CloneTSMappings(seed.Mappings)
		change(&next)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(next.Address(), &next)
		if err := provider.Validate(view.WithState(state), &next); err == nil {
			t.Fatal("forged provider authority admitted", next)
		}
	}
}
func TestTSConfigurationRemap(t *testing.T) {
	e := engine.New()
	d := *e.CatalogRead().MustTSConfiguration(ids.ObjectAddress{ClassID: ids.TSConfigurationClassID, ObjectID: 3748})
	d.ObjectAddress.ObjectID = 91001
	d.ObjectNamespace.ObjectID = 91002
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{d.Address(): {ClassID: ids.TSConfigurationClassID, ObjectID: 92001}, d.Namespace(): {ClassID: ids.NamespaceClassID, ObjectID: 92002}, {ClassID: ids.TSDictionaryClassID, ObjectID: 3765}: {ClassID: ids.TSDictionaryClassID, ObjectID: 92003}}
	mapped := extension.RemapDescriptor(&d, mapping).(*catalog.TSConfiguration)
	if mapped.Address() != mapping[d.Address()] || mapped.Namespace() != mapping[d.Namespace()] || mapped.Parser != d.Parser || mapped.Mapping(1)[0].ObjectID != 92003 || d.Mapping(1)[0].ObjectID != 3765 || &mapped.Mappings[0].Dictionaries[0] == &d.Mappings[0].Dictionaries[0] {
		t.Fatal("configuration remap lost identity/backing ownership", mapped, d)
	}
}
