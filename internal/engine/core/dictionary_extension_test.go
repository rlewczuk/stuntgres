package core_test

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestDictionaryExtensionProviderValidation(t *testing.T) {
	e, _, installation := extensionUnitInstalled(t)
	original := e.CatalogRead()
	dictionary := original.MustTSDictionary(installation.Members[5])
	moved := *dictionary
	moved.ObjectName = "dictionary_member_renamed"
	moved.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	state := catalog.CloneState(original.State())
	state.UnbindName(catalog.DescriptorNameKey(dictionary))
	state.SetDescriptor(moved.Address(), &moved)
	state.BindName(catalog.DescriptorNameKey(&moved), moved.Address())
	view := original.WithState(state)
	provider := extension.CatalogProvider{Class: ids.TSDictionaryClassID}
	if err := provider.Validate(view, &moved); err != nil {
		t.Fatal("mutable name and namespace changed provider authority", err)
	}
	if err := catalog.ValidateExtensionMembership(view, &moved); err != nil {
		t.Fatal(err)
	}
	if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(view, installation); err != nil {
		t.Fatal(err)
	}
	template, ok := view.TSTemplate(dictionary.Template)
	if !ok {
		t.Fatal("installed dictionary lost genuine template")
	}
	if runtime, err := extension.CompileDictionary(context.Background(), view, template, nil, e.TextSearchFiles); runtime != nil || err == nil {
		t.Fatal("missing Rules compiled a fallback dictionary", runtime, err)
	} else {
		extensionUnitError(t, err, "22023")
	}
	for _, change := range []func(*catalog.TSDictionary){
		func(d *catalog.TSDictionary) {
			d.Template = ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}
		},
		func(d *catalog.TSDictionary) { d.Implementation = new(extension.UnaccentProvider) },
		func(d *catalog.TSDictionary) { d.Extension.ObjectID++ },
		func(d *catalog.TSDictionary) { d.Runtime = functions.DefaultSimpleDictionary },
		func(d *catalog.TSDictionary) { d.Options = nil },
		func(d *catalog.TSDictionary) {
			d.Options = []catalog.DictionaryOption{{Name: "rules", Value: "forged", Supplied: true}}
		},
	} {
		candidate := moved
		change(&candidate)
		invalid := catalog.CloneState(state)
		invalid.SetDescriptor(candidate.Address(), &candidate)
		if err := provider.Validate(view.WithState(invalid), &candidate); err == nil {
			t.Fatal("forged dictionary provider admitted", candidate)
		}
	}
	broken := *installation
	broken.Members = append([]ids.ObjectAddress(nil), installation.Members...)
	broken.Members[4], broken.Members[5] = broken.Members[5], broken.Members[4]
	if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(view, &broken); err == nil {
		t.Fatal("nonresident copied extension header admitted")
	}
	reordered := catalog.CloneState(state)
	reordered.SetDescriptor(broken.Address(), &broken)
	if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(view.WithState(reordered), &broken); err != nil {
		t.Fatal("current member ordering became immutable provider authority", err)
	}
	forged := *installation.Installation
	forged.Members = append([]ids.ObjectAddress(nil), forged.Members...)
	forged.Members[4], forged.Members[5] = forged.Members[5], forged.Members[4]
	broken.Installation = &forged
	invalid := catalog.CloneState(state)
	invalid.SetDescriptor(broken.Address(), &broken)
	if err := (extension.CatalogProvider{Class: ids.ExtensionClassID}).Validate(view.WithState(invalid), &broken); err == nil {
		t.Fatal("forged manifest slots admitted")
	}

	principals := authorization.Seed(view.Owner().Principals)
	roles := principals.Read(installation.Owner)
	metadata, err := extension.OwnerMetadata(view, roles, installation)
	if err != nil {
		t.Fatal(err)
	}
	record, found, err := extension.DecodeOwners(strings.TrimPrefix(strings.TrimSpace(metadata), "-- "))
	if err != nil || !found {
		t.Fatal("dictionary owner metadata did not decode", record, found, err)
	}
	member := extensionOwnerDeclarationIndex(t, record, dictionary.Origin.Declaration)
	if record.Members[member].Name != moved.Name() || record.Members[member].Schema != "pg_catalog" {
		t.Fatal("mutable genuine dictionary owner identity did not decode", record)
	}
	candidate := catalog.CloneState(state)
	if changed, err := extension.ImportOwners(context.Background(), view.WithState(candidate), roles, []extension.OwnerRecord{record}); err != nil || changed {
		t.Fatal("exact current dictionary identity did not import", changed, err)
	}
	for _, field := range []string{"name", "schema", "declaration"} {
		bad := record
		bad.Members = append([]extension.MemberOwner(nil), record.Members...)
		switch field {
		case "name":
			bad.Members[member].Name = "unaccent"
		case "schema":
			bad.Members[member].Schema = "public"
		case "declaration":
			bad.Members[member].Declaration = new(len(installation.Installation.Declarations))
		}
		if _, err := extension.ImportOwners(context.Background(), view.WithState(catalog.CloneState(state)), roles, []extension.OwnerRecord{bad}); err == nil {
			t.Fatal("owner metadata bypassed current genuine identity", field)
		}
	}
	if original.MustTSDictionary(dictionary.Address()) != dictionary || dictionary.Name() != "unaccent" || dictionary.Namespace() != installation.TargetNamespace {
		t.Fatal("private candidate modified original dictionary")
	}
}
