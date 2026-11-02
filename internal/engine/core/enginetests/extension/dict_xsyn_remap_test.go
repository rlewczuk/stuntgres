package extension_test

import (
	"slices"
	"strconv"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

func TestDictXsynRemap(t *testing.T) {
	t.Run("stored-datum-replacement", testDictXsynStoredRemap)
	e, op, ext := dictXsynUnitInstalled(t)
	for _, q := range []string{
		"ALTER TEXT SEARCH DICTIONARY xsyn (rules=xsyn_sample,matchsynonyms=true,keeporig=false)",
		"CREATE TEXT SEARCH DICTIONARY dict_xsyn_remap_ordinary (TEMPLATE=xsyn_template,rules=xsyn_sample,matchsynonyms=true,keepsynonyms=false)",
		"COMMENT ON TEXT SEARCH DICTIONARY xsyn IS 'remapped member'",
		"COMMENT ON TEXT SEARCH DICTIONARY dict_xsyn_remap_ordinary IS 'remapped ordinary'",
		"CREATE TEXT SEARCH CONFIGURATION dict_xsyn_remap_cfg (COPY=pg_catalog.simple)",
		"ALTER TEXT SEARCH CONFIGURATION dict_xsyn_remap_cfg ALTER MAPPING FOR asciiword WITH dict_xsyn_remap_ordinary",
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	view := e.CatalogRead()
	ext = view.MustExtension(ext.Address())
	member := view.MustTSDictionary(ext.Installation.Members[3])
	name := func(class ids.OID, value string) ids.ObjectAddress {
		return view.NameAddress(ids.NameKey{ClassID: class, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: value})
	}
	ordinary := view.MustTSDictionary(name(ids.TSDictionaryClassID, "dict_xsyn_remap_ordinary"))
	configuration, ok := view.TSConfiguration(name(ids.TSConfigurationClassID, "dict_xsyn_remap_cfg"))
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
	if md.Comment != "remapped member" || od.Comment != "remapped ordinary" || member.Comment != "remapped member" || ordinary.Comment != "remapped ordinary" {
		t.Fatal("dictionary remap lost current comments or changed source metadata")
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
	if view.State() == mapped.State() || view.Owner() != mapped.Owner() || view.MustTSDictionary(member.Address()) != member || !slices.Equal(member.Options, oldMemberOptions) || !slices.Equal(ordinary.Options, oldOrdinaryOptions) || !member.Runtime.(*functions.XsynDictionary).Configured(true, "xsyn_sample", true, true, false, true) || member.Origin.Installation != ext.Installation {
		t.Fatal("remapping mutated retained source authority or configuration")
	}
}

func testDictXsynStoredRemap(t *testing.T) {
	source, op, ext := dictXsynUnitInstalled(t)
	for _, q := range []string{
		"CREATE TEXT SEARCH DICTIONARY xsyn_stored (TEMPLATE=xsyn_template,rules=xsyn_sample)",
		"CREATE TABLE xsyn_stored_rows(d regdictionary DEFAULT 'xsyn_stored'::regdictionary)",
		"CREATE VIEW xsyn_stored_view AS SELECT d,'xsyn'::regdictionary AS member FROM xsyn_stored_rows",
	} {
		enginetest.RoleExecute(t, source, op, q)
	}
	before := source.CatalogRead()
	public := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	key := func(class ids.OID, name string) ids.NameKey {
		return ids.NameKey{ClassID: class, Namespace: public, Name: name}
	}
	dictionary := before.MustTSDictionary(before.NameAddress(key(ids.TSDictionaryClassID, "xsyn_stored")))
	table := before.MustRelation(before.NameAddress(key(ids.RelationClassID, "xsyn_stored_rows")))
	view := before.MustRelation(before.NameAddress(key(ids.RelationClassID, "xsyn_stored_view")))
	originalDefault := table.ColumnMeta.Defaults[table.Attributes[0]]
	literal, ok := originalDefault.Expression.(plan.LiteralExpr)
	if !ok {
		t.Fatal("stored alias default did not retain its genuine typed datum")
	}
	oid, err := literal.Value.RegTSOID()
	if err != nil || ids.OID(oid) != dictionary.Address().ObjectID {
		t.Fatal("source alias identity", oid, err)
	}

	dest := engine.New()
	destination, done := enginetest.RoleOperation(t, dest, "postgres")
	defer done()
	var collision *catalog.TSDictionary
	for n := uint32(0); n <= uint32(dictionary.Address().ObjectID); n++ {
		name := "xsyn_retained_" + strconv.FormatUint(uint64(n), 10)
		enginetest.RoleExecute(t, dest, destination, "CREATE TEXT SEARCH DICTIONARY pg_temp."+name+" (TEMPLATE=pg_catalog.simple,accept=false)")
		for _, d := range dest.CatalogRead().Descriptors() {
			if candidate, ok := d.(*catalog.TSDictionary); ok && candidate.Name() == name {
				if candidate.Address().ObjectID > dictionary.Address().ObjectID {
					t.Fatal("TEMP dictionary collision overshot source")
				}
				if candidate.Address() == dictionary.Address() {
					collision = candidate
				}
				break
			}
		}
		if collision != nil {
			break
		}
	}
	if collision == nil {
		t.Fatal("actual class-3600 collision not reached")
	}
	if _, err := dest.ReplaceCatalog(t.Context(), source, before); err != nil {
		t.Fatal(err)
	}
	after := dest.CatalogRead()
	imported := after.MustTSDictionary(after.NameAddress(key(ids.TSDictionaryClassID, "xsyn_stored")))
	if imported.Address() == dictionary.Address() || after.Get(collision.Address()) != collision || imported.Runtime != dictionary.Runtime {
		t.Fatal("replacement failed to remap imported identity or changed retained immutable/TEMP state")
	}
	mapped := after.MustRelation(after.NameAddress(key(ids.RelationClassID, "xsyn_stored_rows")))
	mappedDefault := mapped.ColumnMeta.Defaults[mapped.Attributes[0]]
	mappedLiteral, ok := mappedDefault.Expression.(plan.LiteralExpr)
	if !ok {
		t.Fatal("replacement lost typed alias default")
	}
	next, err := mappedLiteral.Value.RegTSOID()
	if err != nil || ids.OID(next) != imported.Address().ObjectID || mapped == table {
		t.Fatal("stored default retained source dictionary identity", next, err)
	}
	mappedView := after.MustRelation(after.NameAddress(key(ids.RelationClassID, "xsyn_stored_view")))
	if mappedView == view || mappedView.View == view.View || mappedView.View.Validate(after, mappedView) != nil {
		t.Fatal("stored view did not acquire destination-owned graph")
	}
	importedExt := after.MustExtension(after.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "dict_xsyn"}))
	for _, address := range importedExt.Installation.Members {
		d := after.Get(address)
		if err := catalog.Validate(after.Owner().Provider(address.ClassID), after, d); err != nil {
			t.Fatal("destination installed graph", err)
		}
	}
	retained := table.ColumnMeta.Defaults[table.Attributes[0]].Expression.(plan.LiteralExpr)
	sourceOID, err := retained.Value.RegTSOID()
	if err != nil || sourceOID != oid || source.CatalogRead().State() != before.State() || before.MustTSDictionary(ext.Members[3]).Origin.Installation != ext.Installation || source.CatalogRead().MustRelation(table.Address()) != table || before.MustTSDictionary(dictionary.Address()) != dictionary {
		t.Fatal("replacement mutated retained source graph/default/authority")
	}
}
