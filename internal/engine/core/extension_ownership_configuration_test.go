package core_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

func ownershipConfigurationIdentity(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "unaccent")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, `CREATE TEXT SEARCH CONFIGURATION public.eo_config (COPY=pg_catalog.simple)`)
	enginetest.RoleExecute(t, e, op, `CREATE TEXT SEARCH DICTIONARY public.eo_config_dict (TEMPLATE=pg_catalog.simple)`)
	enginetest.RoleExecute(t, e, op, `ALTER TEXT SEARCH CONFIGURATION public.eo_config ALTER MAPPING FOR asciiword WITH public.eo_config_dict`)
	before := e.CatalogRead()
	a := before.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: ext.TargetNamespace, Name: "eo_config"})
	original := before.MustTSConfiguration(a)
	if len(original.Mappings) != 19 || original.Extension != (ids.ObjectAddress{}) {
		t.Fatal("unexpected genuine copied configuration", original)
	}
	closure, err := catalog.ExtensionMemberClosure(before, original)
	if err != nil || len(closure) != 1 || closure[0] != original {
		t.Fatal("mapping dependencies became direct companion membership", closure, err)
	}
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent ADD TEXT SEARCH CONFIGURATION public.eo_config`)
	attachedView := e.CatalogRead()
	attached := attachedView.MustTSConfiguration(a)
	if attached == original || attached.Extension != ext.Address() || attached.Parser != original.Parser || attached.Owner != original.Owner || &attached.Mappings[0] != &original.Mappings[0] {
		t.Fatal("membership copied/mutated immutable configuration payload")
	}
	if err := (catalog.TSConfigurationProvider{}).Validate(attachedView, attached); err != nil {
		t.Fatal(err)
	}
	candidate := catalog.CloneState(attachedView.State())
	broken := *attachedView.MustExtension(ext.Address())
	broken.Members = slices.Clone(broken.Members[:len(broken.Members)-1])
	candidate.SetDescriptor(broken.Address(), &broken)
	if err := (catalog.TSConfigurationProvider{}).Validate(attachedView.WithState(candidate), attached); err == nil {
		t.Fatal("missing reciprocal configuration member accepted")
	}
	enginetest.RoleExecute(t, e, op, `CREATE SCHEMA eo_config_destination`)
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent SET SCHEMA eo_config_destination`)
	moved := e.CatalogRead().MustTSConfiguration(a)
	if moved.Namespace() == original.Namespace() || moved.Extension != ext.Address() || moved.Parser != original.Parser || moved.Owner != original.Owner || &moved.Mappings[0] != &original.Mappings[0] || before.MustTSConfiguration(a) != original {
		t.Fatal("mechanical move changed mapping/parser/owner or captured predecessor")
	}
	dictionary := before.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ext.TargetNamespace, Name: "eo_config_dict"})
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 940001}, a: {ClassID: ids.TSConfigurationClassID, ObjectID: 940002}, moved.Namespace(): {ClassID: ids.NamespaceClassID, ObjectID: 940003}}
	mapping[dictionary] = ids.ObjectAddress{ClassID: ids.TSDictionaryClassID, ObjectID: 940004}
	remapped := extension.RemapDescriptor(moved, mapping).(*catalog.TSConfiguration)
	if remapped.Address() != mapping[a] || remapped.Extension != mapping[ext.Address()] || remapped.Namespace() != mapping[moved.Namespace()] || remapped.Owner != moved.Owner || remapped.Parser != moved.Parser || len(remapped.Mappings) != len(moved.Mappings) {
		t.Fatal("configuration remap lost logical membership or mapping identity")
	}
	found := false
	for i, m := range moved.Mappings {
		if remapped.Mappings[i].TokenID != m.TokenID || len(remapped.Mappings[i].Dictionaries) != len(m.Dictionaries) {
			t.Fatal("configuration remap changed mapping shape")
		}
		for j, source := range m.Dictionaries {
			want := source
			if next, ok := mapping[source]; ok {
				want = next
				found = true
			}
			if remapped.Mappings[i].Dictionaries[j] != want {
				t.Fatal("configuration remap lost ordered dictionary identity")
			}
		}
	}
	if !found || before.MustTSConfiguration(a).Mappings[0].TokenID != original.Mappings[0].TokenID {
		t.Fatal("genuine custom dictionary remap was not exercised")
	}
}
