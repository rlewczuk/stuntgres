package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
)

func TestTemplateOriginMembershipAuthority(t *testing.T) {
	for _, mode := range []string{"detached", "foreign", "absent_origin", "ordinary_member", "builtin_member"} {
		t.Run(mode, func(t *testing.T) {
			e, op, installation := extensionUnitInstalled(t)
			ordinary := templateUnitStandalone(t, e, op, "ordinary_callback_template")
			v := e.CatalogRead()
			state := catalog.CloneState(v.State())
			parent := *installation
			parent.Members = append([]ids.ObjectAddress(nil), installation.Members...)
			installed := *v.Get(installation.Members[4]).(*catalog.TSTemplate)
			template := &installed
			options := []catalog.DictionaryOption{{Name: "rules", Value: "unaccent", Supplied: true}}
			receiver := *v.MustExtension(v.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "plpgsql"}))
			receiver.Members = append([]ids.ObjectAddress(nil), receiver.Members...)
			switch mode {
			case "ordinary_member":
				copy := *ordinary
				copy.Extension = parent.Address()
				template = &copy
				parent.Members = append(parent.Members, copy.Address())
			case "builtin_member":
				template = v.Get(ids.ObjectAddress{ClassID: ids.TSTemplateClassID, ObjectID: 3727}).(*catalog.TSTemplate)
				options = nil
				for _, a := range []ids.ObjectAddress{template.Init, template.Lexize} {
					p := *v.MustProcedure(a)
					p.Extension = parent.Address()
					state.SetDescriptor(a, &p)
					parent.Members = append(parent.Members, a)
				}
			default:
				installed.Extension = ids.ObjectAddress{}
				if mode == "foreign" {
					installed.Extension = receiver.Address()
					receiver.Members = append(receiver.Members, installed.Address())
					state.SetDescriptor(receiver.Address(), &receiver)
				}
				for _, a := range installation.Members[2:4] {
					p := *v.MustProcedure(a)
					p.Extension = ids.ObjectAddress{}
					state.SetDescriptor(a, &p)
				}
				parent.Members = []ids.ObjectAddress{installation.Members[0], installation.Members[1], installation.Members[5]}
				if mode == "absent_origin" {
					for _, a := range parent.Members {
						state.RemoveDescriptor(a)
					}
					state.UnbindName(catalog.DescriptorNameKey(installation))
					state.RemoveDescriptor(installation.Address())
				}
			}
			if mode != "absent_origin" {
				state.SetDescriptor(parent.Address(), &parent)
			}
			state.SetDescriptor(template.Address(), template)
			current := v.WithState(state)
			runtime, err := extension.CompileDictionary(t.Context(), current, template, options, e.TextSearchFiles)
			if err != nil || runtime == nil {
				t.Fatal(mode, runtime, err)
			}
			if mode != "builtin_member" && runtime != functions.StockUnaccent {
				t.Fatal("lost unaccent authority", mode)
			}
			if mode == "builtin_member" {
				if _, ok := runtime.(*functions.SimpleDictionary); !ok {
					t.Fatal("lost intrinsic authority")
				}
			}
		})
	}
}

func TestTemplateCallbackOriginNegatives(t *testing.T) {
	e, op, installation := extensionUnitInstalled(t)
	template := templateUnitStandalone(t, e, op, "callback_origin_negatives")
	v := e.CatalogRead()
	foreign, _, foreignInstallation := extensionUnitInstalled(t)
	foreignProcedure := foreign.CatalogRead().MustProcedure(foreignInstallation.Members[2])
	for _, mode := range []string{"copied_origin", "absent_origin", "source", "principal", "cohort", "generation"} {
		t.Run(mode, func(t *testing.T) {
			p := *v.MustProcedure(installation.Members[2])
			switch mode {
			case "copied_origin":
				recipe := *p.Origin.Installation
				origin := *p.Origin
				origin.Installation = &recipe
				p.Origin = &origin
			case "absent_origin":
				p.Origin = nil
			case "source":
				p.Display.Source = "forged_callback"
			case "principal":
				p.Owner = foreignProcedure.Owner
			case "cohort":
				// An independently reconstructed genuine recipe with the same
				// logical addresses is not foreign authority. Give this genuine
				// origin a distinct cohort while preserving the callback address.
				cohort := foreignInstallation.Address()
				distinct := cohort
				distinct.ObjectID += 100000
				remapped := extension.RemapDescriptor(foreignProcedure, map[ids.ObjectAddress]ids.ObjectAddress{cohort: distinct}).(*catalog.Procedure)
				p.Origin = remapped.Origin
			case "generation":
				p.Installed.Parallel ^= 1
			}
			state := catalog.CloneState(v.State())
			state.SetDescriptor(p.Address(), &p)
			if runtime, err := extension.CompileDictionary(t.Context(), v.WithState(state), template, []catalog.DictionaryOption{{Name: "rules", Value: "unaccent", Supplied: true}}, e.TextSearchFiles); err == nil || runtime != nil {
				t.Fatal("invalid callback authority admitted", mode, runtime, err)
			}
		})
	}
}
