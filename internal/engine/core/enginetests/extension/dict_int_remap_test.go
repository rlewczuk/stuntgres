package extension_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestDictIntControlsRemap(t *testing.T) {
	e, op, ext := dictIntUnitInstalled(t)
	for _, q := range []string{
		"ALTER TEXT SEARCH DICTIONARY intdict (maxlen=3,absval=true)",
		"CREATE TEXT SEARCH DICTIONARY dict_int_remap_ordinary (TEMPLATE=intdict_template,maxlen=2,rejectlong=true)",
		"CREATE TEXT SEARCH CONFIGURATION dict_int_remap_cfg (COPY=pg_catalog.simple)",
		"ALTER TEXT SEARCH CONFIGURATION dict_int_remap_cfg ALTER MAPPING FOR int,uint WITH dict_int_remap_ordinary",
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	view := e.CatalogRead()
	ext = view.MustExtension(ext.Address())
	member := view.MustTSDictionary(ext.Installation.Members[3])
	name := func(class ids.OID, value string) ids.ObjectAddress {
		return view.NameAddress(ids.NameKey{ClassID: class, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: value})
	}
	ordinary := view.MustTSDictionary(name(ids.TSDictionaryClassID, "dict_int_remap_ordinary"))
	configuration, ok := view.TSConfiguration(name(ids.TSConfigurationClassID, "dict_int_remap_cfg"))
	if !ok {
		t.Fatal("configuration missing")
	}
	oldMemberOptions, oldOrdinaryOptions := slices.Clone(member.Options), slices.Clone(ordinary.Options)
	mapping := make(map[ids.ObjectAddress]ids.ObjectAddress)
	for _, address := range append(append(slices.Clone(ext.Installation.Members), ext.Address()), ordinary.Address(), configuration.Address()) {
		next := address
		next.ObjectID += 1000000
		mapping[address] = next
	}
	remap := extension.DescriptorRemapper(mapping)
	state := catalog.CloneState(view.State())
	for address, descriptor := range view.Descriptors() {
		next := remap(descriptor)
		if next == descriptor {
			continue
		}
		state.UnbindName(catalog.DescriptorNameKey(descriptor))
		state.RemoveDescriptor(address)
		state.SetDescriptor(next.Address(), next)
		state.BindName(catalog.DescriptorNameKey(next), next.Address())
	}
	mapped := view.WithState(state)
	imported := mapped.MustExtension(mapping[ext.Address()])
	md := mapped.MustTSDictionary(mapping[member.Address()])
	od := mapped.MustTSDictionary(mapping[ordinary.Address()])
	if imported.Installation == ext.Installation || !imported.Installation.Valid() || imported.Installation.Cohort != imported.Address() || md.Origin.Installation != imported.Installation || md.Origin.Declaration != 3 || md.Runtime != member.Runtime || od.Runtime != ordinary.Runtime || md.Template != mapping[member.Template] || od.Template != mapping[ordinary.Template] || &md.Options[0] != &member.Options[0] || &od.Options[0] != &ordinary.Options[0] {
		t.Fatal("remap lost destination cohort or immutable runtime/options backing")
	}
	for _, address := range append(slices.Clone(ext.Installation.Members), ext.Address(), ordinary.Address(), configuration.Address()) {
		next := mapped.Get(mapping[address])
		if next == nil || next == view.Get(address) || mapped.Get(address) != nil {
			t.Fatal("source identity survived destination remap", address, next)
		}
		if err := catalog.Validate(mapped.Owner().Provider(next.Address().ClassID), mapped, next); err != nil {
			t.Fatal("remapped genuine graph rejected", address, err)
		}
		if origin := catalog.DescriptorOrigin(next); origin != nil && origin.Installation != imported.Installation {
			t.Fatal("installed siblings did not share one sealed remapped recipe", address)
		}
	}
	mt, _ := mapped.TSTemplate(md.Template)
	if mt.Init != mapping[ext.Installation.Members[0]] || mt.Lexize != mapping[ext.Installation.Members[1]] || od.Implementation != nil || od.Origin != nil {
		t.Fatal("template callbacks or ordinary dictionary acquired wrong origin")
	}
	mc, _ := mapped.TSConfiguration(mapping[configuration.Address()])
	if mc == configuration || len(mc.Mappings) != len(configuration.Mappings) {
		t.Fatal("configuration capture was not remapped independently")
	}
	for i, old := range configuration.Mappings {
		for j, address := range old.Dictionaries {
			want := address
			if next, ok := mapping[address]; ok {
				want = next
			}
			if mc.Mappings[i].Dictionaries[j] != want || &mc.Mappings[i].Dictionaries[j] == &old.Dictionaries[j] {
				t.Fatal("configuration mappings retained mutable source identity/backing")
			}
		}
	}
	if view.State() == mapped.State() || view.Owner() != mapped.Owner() || view.MustTSDictionary(member.Address()) != member || !slices.Equal(member.Options, oldMemberOptions) || !slices.Equal(ordinary.Options, oldOrdinaryOptions) || !member.Runtime.(*functions.IntDictionary).Configured(3, false, true) || member.Origin.Installation != ext.Installation {
		t.Fatal("remapping mutated retained source authority or configuration")
	}
}
