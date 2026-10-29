package core_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func ownershipSnowballIdentity(t *testing.T) {
	e, first := ownershipUnitInstall(t, "unaccent")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	before := e.CatalogRead()
	dictionary := snowballUnitDictionary(t, e)
	template, ok := before.TSTemplate(dictionary.Template)
	if !ok {
		t.Fatal("missing genuine Snowball template")
	}
	configuration := before.MustTSConfiguration(before.NameAddress(ids.NameKey{ClassID: ids.TSConfigurationClassID, Namespace: dictionary.Namespace(), Name: "english"}))
	addresses := []ids.ObjectAddress{template.Init, template.Lexize, template.Address(), dictionary.Address(), configuration.Address()}
	for _, sql := range []string{
		`ALTER EXTENSION unaccent ADD FUNCTION pg_catalog.dsnowball_init(internal)`,
		`ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY pg_catalog.english_stem`,
		`ALTER EXTENSION plpgsql ADD TEXT SEARCH TEMPLATE pg_catalog.snowball`,
		`ALTER EXTENSION plpgsql ADD TEXT SEARCH CONFIGURATION pg_catalog.english`,
	} {
		enginetest.RoleExecute(t, e, op, sql)
	}
	view := e.CatalogRead()
	second := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "plpgsql"}))
	for _, a := range addresses {
		old, current := before.Get(a), view.Get(a)
		if stored.DependPinned(view, nil, a) || catalog.DescriptorOrigin(current) != nil {
			t.Fatal("membership replaced intrinsic seed authority", a)
		}
		if err := catalog.Validate(view.Owner().Provider(a.ClassID), view, current); err != nil {
			t.Fatal("lawful distinct receiver/detached seed rejected", a, err)
		}
		if a != template.Lexize && (current == old || current.Revision() == old.Revision()) {
			t.Fatal("membership did not publish a new immutable generation", a)
		}
		if catalog.ExtensionMembership(old) != (ids.ObjectAddress{}) {
			t.Fatal("membership mutated captured seed", a)
		}
	}
	for _, sql := range []string{
		`DROP FUNCTION pg_catalog.dsnowball_init(internal) CASCADE`,
		`DROP FUNCTION pg_catalog.dsnowball_lexize(internal,internal,internal,internal) RESTRICT`,
		`DROP TEXT SEARCH TEMPLATE pg_catalog.snowball CASCADE`,
		`DROP TEXT SEARCH DICTIONARY pg_catalog.english_stem CASCADE`,
		`DROP TEXT SEARCH CONFIGURATION pg_catalog.english CASCADE`,
	} {
		err := extensionUnitExecute(t, e, op, enginetest.Statement(t, sql))
		extensionUnitError(t, err, "2BP01")
		if strings.Contains(err.Error(), "required by the database system") {
			t.Fatal("allocated Snowball seed incorrectly pinned", err)
		}
		for a, old := range view.Descriptors() {
			if e.CatalogRead().Get(a) != old {
				t.Fatal("restricted drop changed immutable source", a)
			}
		}
	}
	if view.MustTSDictionary(dictionary.Address()).Runtime != dictionary.Runtime || &view.MustTSDictionary(dictionary.Address()).Options[0] != &dictionary.Options[0] || &view.MustTSConfiguration(configuration.Address()).Mappings[0] != &configuration.Mappings[0] {
		t.Fatal("membership replaced immutable runtime/options/mapping backing")
	}
	// Intrinsic execution authority ignores mutable e; class admission still
	// rejects dangling parents and missing reverse membership.
	for _, a := range []ids.ObjectAddress{template.Init, template.Address(), dictionary.Address(), configuration.Address()} {
		state := catalog.CloneState(view.State())
		parent := *view.MustExtension(catalog.ExtensionMembership(view.Get(a)))
		parent.Members = slices.DeleteFunc(slices.Clone(parent.Members), func(member ids.ObjectAddress) bool { return member == a })
		state.SetDescriptor(parent.Address(), &parent)
		if err := catalog.Validate(view.Owner().Provider(a.ClassID), view.WithState(state), state.Get(a)); err == nil {
			t.Fatal("missing reverse current parent admitted", a)
		}
		state.RemoveDescriptor(parent.Address())
		if err := catalog.Validate(view.Owner().Provider(a.ClassID), view.WithState(state), state.Get(a)); err == nil {
			t.Fatal("dangling current parent admitted", a)
		}
	}
	// One memo remaps both receivers and every intrinsic link; runtime and
	// immutable option backing stay catalog-free rather than being serialized.
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{}
	for i, a := range append(slices.Clone(addresses), first.Address(), second.Address()) {
		mapping[a] = ids.ObjectAddress{ClassID: a.ClassID, ObjectID: ids.OID(970000 + i)}
	}
	remap := extension.DescriptorRemapper(mapping)
	state := catalog.CloneState(view.State())
	for a, d := range view.Descriptors() {
		next := remap(d)
		if next != d {
			state.UnbindName(catalog.DescriptorNameKey(d))
			state.RemoveDescriptor(a)
			state.SetDescriptor(next.Address(), next)
			state.BindName(catalog.DescriptorNameKey(next), next.Address())
		}
	}
	mapped := view.WithState(state)
	for _, a := range addresses {
		d := mapped.Get(mapping[a])
		if err := catalog.Validate(mapped.Owner().Provider(a.ClassID), mapped, d); err != nil {
			t.Fatal("remapped intrinsic graph rejected", a, err)
		}
		if stored.DependPinned(mapped, nil, d.Address()) {
			t.Fatal("allocated remapped seed acquired pin", a)
		}
	}
	md := mapped.MustTSDictionary(mapping[dictionary.Address()])
	mt, ok := mapped.TSTemplate(mapping[template.Address()])
	if !ok {
		t.Fatal("missing remapped Snowball template")
	}
	mc := mapped.MustTSConfiguration(mapping[configuration.Address()])
	if md.Template != mt.Address() || mt.Init != mapping[template.Init] || mt.Lexize != mapping[template.Lexize] || md.Runtime != dictionary.Runtime || mc.Mapping(1)[0] != md.Address() {
		t.Fatal("remap lost helper/template/dictionary/configuration links")
	}
	for _, parent := range []*catalog.Extension{view.MustExtension(first.Address()), second} {
		text, err := extension.OwnerMetadata(view, e.Roles(), parent)
		if err != nil {
			t.Fatal(err)
		}
		record, found, err := extension.DecodeOwners(strings.TrimSpace(strings.TrimPrefix(text, "-- ")))
		if err != nil || !found {
			t.Fatal(found, err)
		}
		candidate := catalog.CloneState(view.State())
		if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), e.Roles(), []extension.OwnerRecord{record}); changed || err != nil {
			t.Fatal("ordinary intrinsic membership import", changed, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		canceled := catalog.CloneState(view.State())
		if changed, err := extension.ImportOwners(ctx, view.WithState(canceled), e.Roles(), []extension.OwnerRecord{record}); changed || err != context.Canceled {
			t.Fatal("canceled intrinsic membership import", changed, err)
		}
		for a, old := range view.Descriptors() {
			if canceled.Get(a) != old {
				t.Fatal("canceled import changed immutable source", a)
			}
		}
		for i, member := range record.Members {
			if member.Declaration != nil {
				continue
			}
			if member.Version != "" || member.Owner != "" || member.ACL != nil {
				t.Fatal("intrinsic seed manufactured installed origin", member)
			}
			for _, field := range []string{"version", "owner", "acl", "declaration"} {
				bad := record
				bad.Members = slices.Clone(record.Members)
				switch field {
				case "version":
					bad.Members[i].Version = parent.Version
				case "owner":
					bad.Members[i].Owner = "postgres"
				case "acl":
					bad.Members[i].ACL = &extension.MemberACL{}
				case "declaration":
					n := 999
					bad.Members[i].Declaration, bad.Members[i].Version = &n, parent.Version
				}
				data, err := json.Marshal(bad)
				if err != nil {
					t.Fatal(err)
				}
				_, _, decodeErr := extension.DecodeOwners(extension.OwnersMarker + " " + base64.StdEncoding.EncodeToString(data))
				if field != "declaration" && decodeErr == nil {
					t.Fatal("ordinary-v3 forged authority decoded", field)
				}
				candidate := catalog.CloneState(view.State())
				if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), e.Roles(), []extension.OwnerRecord{bad}); changed || err == nil {
					t.Fatal("ordinary-v3 forged authority imported", field, changed, err)
				}
				for a, old := range view.Descriptors() {
					if candidate.Get(a) != old {
						t.Fatal("rejected import changed backing", a)
					}
				}
			}
		}
	}
	enginetest.RoleExecute(t, e, op, `ALTER EXTENSION unaccent DROP FUNCTION pg_catalog.dsnowball_init(internal)`)
	if current := e.CatalogRead().MustProcedure(template.Init); current.Extension != (ids.ObjectAddress{}) || catalog.ValidateSnowballHelper(e.CatalogRead(), current) != nil || current.SnowballHelper != catalog.SnowballInit {
		t.Fatal("lawful local clearing lost helper authority")
	}
}
