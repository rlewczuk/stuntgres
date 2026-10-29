package core_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func TestSynonymProvider(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	enginetest.SessionSQL(t, s, "CREATE TEXT SEARCH DICTIONARY public.synonym_provider (TEMPLATE=pg_catalog.synonym,Synonyms=synonym_sample)")
	view := e.CatalogRead()
	address := view.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "synonym_provider"})
	dictionary := view.MustTSDictionary(address)
	template, ok := view.TSTemplate(dictionary.Template)
	if !ok || !template.Seeded() || template.Address().ObjectID != 3730 || template.Init.ObjectID != 3728 || template.Lexize.ObjectID != 3729 || dictionary.Seeded() {
		t.Fatal("genuine synonym identities", template, dictionary)
	}
	provider := extension.CatalogProvider{Class: ids.TSDictionaryClassID}
	if err := provider.Validate(view, dictionary); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*catalog.TSDictionary){
		func(d *catalog.TSDictionary) { d.Runtime = nil },
		func(d *catalog.TSDictionary) { d.Runtime = functions.DefaultSimpleDictionary },
		func(d *catalog.TSDictionary) { d.Owner = view.Owner().Principals.Principal(0) },
		func(d *catalog.TSDictionary) {
			d.Options = []catalog.DictionaryOption{{Name: "synonyms", Value: "different", Supplied: true}}
		},
		func(d *catalog.TSDictionary) {
			d.Options = append([]catalog.DictionaryOption{{Name: "casesensitive", Value: "true", Supplied: true}}, d.Options...)
		},
		func(d *catalog.TSDictionary) {
			d.Extension = ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 123456}
		},
		func(d *catalog.TSDictionary) { d.Template.ObjectID = 3727 },
	} {
		bad := *dictionary
		mutate(&bad)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(bad.Address(), &bad)
		if err := provider.Validate(view.WithState(state), &bad); err == nil {
			t.Fatal("forged runtime/configuration accepted", bad)
		}
	}
	// An authentic helper descriptor is required, not merely an admitted
	// signature, a matching template name, or a SynonymDictionary type assertion.
	for _, mutate := range []func(*catalog.Procedure){
		func(p *catalog.Procedure) { p.Builtin = nil },
		func(p *catalog.Procedure) { p.Result = 25 },
		func(p *catalog.Procedure) { p.Signature = catalog.UnaccentLexizeSignature },
		func(p *catalog.Procedure) { p.Strict = !p.Strict },
		func(p *catalog.Procedure) { p.Volatility++ },
		func(p *catalog.Procedure) {
			copied := *p.Builtin
			p.Builtin = &copied
		},
		func(p *catalog.Procedure) { p.Owner = view.Owner().Principals.Principal(0) },
		func(p *catalog.Procedure) { p.Implementation = extension.StockUnaccent },
		func(p *catalog.Procedure) { p.Foreign = &catalog.ForeignProcedure{} },
		func(p *catalog.Procedure) { p.Origin = &catalog.InstalledOrigin{} },
		func(p *catalog.Procedure) {
			p.Extension = ids.ObjectAddress{ClassID: ids.ExtensionClassID, ObjectID: 123456}
		},
	} {
		original, _ := view.Procedure(template.Init)
		helper := *original
		mutate(&helper)
		state := catalog.CloneState(view.State())
		state.SetDescriptor(helper.Address(), &helper)
		forged := view.WithState(state)
		if _, err := extension.CompileDictionary(context.Background(), forged, template, dictionary.Options); err == nil {
			t.Fatal("forged helper admitted", helper)
		}
		if err := provider.Validate(forged, dictionary); err == nil {
			t.Fatal("forged helper validated", helper)
		}
	}
	// Repeated synonyms are intentionally last-wins, unlike simple's options.
	options := []catalog.DictionaryOption{{Name: "synonyms", Value: "synonym_missing", Supplied: true}, {Name: "synonyms", Value: "synonym_sample", Supplied: true}, {Name: "casesensitive", Value: "1", Numeric: true, Supplied: true}}
	runtime, err := extension.CompileDictionary(context.Background(), view, template, options)
	if err != nil {
		t.Fatal(err)
	}
	compiled, ok := runtime.(*functions.SynonymDictionary)
	if !ok || !compiled.Configured("synonym_sample", true) {
		t.Fatal("ordered options lost", runtime)
	}
	result, err := compiled.Lexize(context.Background(), "POSTGRES")
	if err != nil || result.Recognized {
		t.Fatal("sensitive compiled runtime", result, err)
	}
	result, err = compiled.Lexize(context.Background(), "indices")
	if err != nil || !result.Recognized || len(result.Lexemes) != 1 || result.Lexemes[0].Text != "index" || !result.Lexemes[0].Prefix || result.Lexemes[0].Variant != 0 {
		t.Fatal("real provider prefix", result, err)
	}
}

// Fixed intrinsic callbacks/template retain pin authority, not installation
// provenance. Their current receivers need not agree with an ordinary dictionary.
func TestSynonymMembershipIdentity(t *testing.T) {
	e, first := ownershipUnitInstall(t, "unaccent")
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, `CREATE TEXT SEARCH DICTIONARY public.synonym_member (TEMPLATE=pg_catalog.synonym,Synonyms=synonym_sample)`)
	before := e.CatalogRead()
	address := before.NameAddress(ids.NameKey{ClassID: ids.TSDictionaryClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "synonym_member"})
	dictionary := before.MustTSDictionary(address)
	template, ok := before.TSTemplate(dictionary.Template)
	if !ok {
		t.Fatal("missing genuine synonym template")
	}
	for _, q := range []string{
		`ALTER EXTENSION unaccent ADD FUNCTION pg_catalog.dsynonym_init(internal)`,
		`ALTER EXTENSION plpgsql ADD TEXT SEARCH TEMPLATE pg_catalog.synonym`,
		`ALTER EXTENSION unaccent ADD TEXT SEARCH DICTIONARY public.synonym_member`,
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	view := e.CatalogRead()
	second := view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "plpgsql"}))
	addresses := []ids.ObjectAddress{template.Init, template.Lexize, template.Address(), address}
	for _, a := range addresses {
		current := view.Get(a)
		if catalog.DescriptorOrigin(current) != nil {
			t.Fatal("intrinsic/ordinary member acquired installation provenance", a)
		}
		if err := catalog.Validate(view.Owner().Provider(a.ClassID), view, current); err != nil {
			t.Fatal("lawful mixed current membership rejected", a, err)
		}
		if stored.DependPinned(view, nil, a) != (a != address) {
			t.Fatal("fixed pin confused with ordinary dictionary or allocated seed", a)
		}
		if catalog.ExtensionMembership(before.Get(a)) != (ids.ObjectAddress{}) {
			t.Fatal("membership mutated captured descriptor", a)
		}
		if a != template.Lexize && (current == before.Get(a) || current.Revision() == before.Get(a).Revision()) {
			t.Fatal("membership failed to publish a new immutable generation", a)
		}
	}
	current := view.MustTSDictionary(address)
	if current.Runtime != dictionary.Runtime || &current.Options[0] != &dictionary.Options[0] {
		t.Fatal("membership copied immutable runtime/options backing")
	}
	for _, a := range []ids.ObjectAddress{template.Init, template.Address(), address} {
		state := catalog.CloneState(view.State())
		parent := *view.MustExtension(catalog.ExtensionMembership(view.Get(a)))
		parent.Members = slices.DeleteFunc(slices.Clone(parent.Members), func(member ids.ObjectAddress) bool { return member == a })
		state.SetDescriptor(parent.Address(), &parent)
		if err := catalog.Validate(view.Owner().Provider(a.ClassID), view.WithState(state), state.Get(a)); err == nil {
			t.Fatal("missing reciprocal membership accepted", a)
		}
		state.RemoveDescriptor(parent.Address())
		if err := catalog.Validate(view.Owner().Provider(a.ClassID), view.WithState(state), state.Get(a)); err == nil {
			t.Fatal("dangling current receiver accepted", a)
		}
	}
	// Remap the allocated dictionary and both actual receivers with one memo.
	// Fixed bootstrap identities are deliberately not reallocated.
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		address:          {ClassID: address.ClassID, ObjectID: 971000},
		first.Address():  {ClassID: ids.ExtensionClassID, ObjectID: 971001},
		second.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 971002},
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
	md := mapped.MustTSDictionary(mapping[address])
	if md.Template != template.Address() || md.Extension != mapping[first.Address()] || md.Runtime != dictionary.Runtime || &md.Options[0] != &dictionary.Options[0] {
		t.Fatal("remap lost current membership, fixed template or immutable backing")
	}
	for _, a := range []ids.ObjectAddress{template.Init, template.Lexize, template.Address(), md.Address()} {
		if err := catalog.Validate(mapped.Owner().Provider(a.ClassID), mapped, mapped.Get(a)); err != nil {
			t.Fatal("remapped synonym graph rejected", a, err)
		}
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
			t.Fatal("ordinary synonym v3 membership import", changed, err)
		}
		for i, member := range record.Members {
			if member.Declaration != nil {
				continue
			}
			if member.Version != "" || member.Owner != "" || member.ACL != nil {
				t.Fatal("ordinary synonym member acquired installed authority", member)
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
				candidate := catalog.CloneState(view.State())
				if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), e.Roles(), []extension.OwnerRecord{bad}); changed || err == nil {
					t.Fatal("forged synonym v3 authority accepted", field, changed, err)
				}
				for a, old := range view.Descriptors() {
					if candidate.Get(a) != old {
						t.Fatal("failed v3 import changed immutable graph", a)
					}
				}
			}
		}
	}
	for _, q := range []string{
		`ALTER EXTENSION unaccent DROP FUNCTION pg_catalog.dsynonym_init(internal)`,
		`ALTER EXTENSION plpgsql DROP TEXT SEARCH TEMPLATE pg_catalog.synonym`,
		`ALTER EXTENSION unaccent DROP TEXT SEARCH DICTIONARY public.synonym_member`,
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	detached := e.CatalogRead()
	for _, a := range addresses {
		if catalog.ExtensionMembership(detached.Get(a)) != (ids.ObjectAddress{}) {
			t.Fatal("detach retained e", a)
		}
		if err := catalog.Validate(detached.Owner().Provider(a.ClassID), detached, detached.Get(a)); err != nil {
			t.Fatal("detached intrinsic capability rejected", a, err)
		}
	}
	enginetest.RoleExecute(t, e, op, `DROP EXTENSION unaccent`)
	withoutOriginHeader := e.CatalogRead()
	detachedTemplate, ok := withoutOriginHeader.TSTemplate(template.Address())
	if !ok {
		t.Fatal("dropping former receiver consumed detached intrinsic template")
	}
	if _, err := extension.CompileDictionary(context.Background(), withoutOriginHeader, detachedTemplate, dictionary.Options); err != nil {
		t.Fatal("intrinsic capability required a live former receiver", err)
	}
}
