package enginetests_test

import (
	"fmt"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestPgTrgmStoredRemap(t *testing.T) {
	source, target := engine.New(), engine.New()
	// Advance the actual destination allocator before installation, rather than
	// manufacturing equal-address descriptors or a second provider inventory.
	target.Mu.Lock()
	_, err := target.Catalog.AllocateOIDLocked()
	target.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	ss, ts := pgTrgmOwnerInstall(t, source), pgTrgmOwnerInstall(t, target)
	bound := pgTrgmOwnerProjection(t, source, ss)
	_ = pgTrgmOwnerProjection(t, target, ts)
	sv, tv := source.CatalogRead(), target.CatalogRead()
	installed := func(view catalog.Read) *catalog.Extension {
		return view.MustExtension(view.NameAddress(ids.NameKey{ClassID: ids.ExtensionClassID, Name: "pg_trgm"}))
	}
	se, te := installed(sv), installed(tv)
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{se.Address(): te.Address()}
	for i, address := range se.Installation.Members {
		mapping[address] = te.Installation.Members[i]
	}
	relation := func(view catalog.Read) ids.ObjectAddress {
		return view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: "pgtrgm_owner"})
	}
	mapping[relation(sv)] = relation(tv)
	address := func(old ids.ObjectAddress) ids.ObjectAddress {
		if next, ok := mapping[old]; ok {
			return next
		}
		return old
	}
	reference := func(old ids.ObjectRef) ids.ObjectRef {
		if old == (ids.ObjectRef{}) {
			return old
		}
		return tv.Owner().Domain.Ref(tv.Epoch(), address(old.Address))
	}
	types := func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == nil {
			return nil
		}
		if next, ok := tv.LookupType(old.ID()); ok {
			return next
		}
		return old
	}
	principal := func(old authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if old != (authorization.PrincipalRef{}) && old != sv.Owner().Principals.Principal(old.ID()) {
			return authorization.PrincipalRef{}, fmt.Errorf("foreign source principal")
		}
		return tv.Owner().Principals.Principal(old.ID()), nil
	}
	principals := catalog.NewProcedureRemapper(catalog.PrincipalRemap{Principal: principal, SeededOwner: func(old authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
		return principal(old)
	}})
	procedure := func(old *catalog.Procedure) (*catalog.Procedure, error) {
		if old == nil {
			return nil, nil
		}
		if err := extension.StockPgTrgm.ValidateProcedure(sv, old); err != nil {
			return nil, err
		}
		return principals(extension.RemapDescriptor(old, mapping).(*catalog.Procedure))
	}
	original := make([]functions.PgTrgm, len(bound.Expressions))
	for i, node := range bound.Expressions {
		original[i] = pgTrgmOwnerCall(t, node)
		original[i].Args = slices.Clone(original[i].Args)
	}
	mapper := stored.ViewQueryRemapper{Catalog: tv, Ref: reference, Type: types, Procedure: procedure}
	mapped, err := mapper.Nested(bound)
	if err != nil {
		t.Fatal("authorized actual installation remap", err)
	}
	project, ok := mapped.(*plan.ProjectNode)
	if !ok || len(project.Expressions) != len(original) {
		t.Fatal("lost complete remapped projection")
	}
	for i, node := range project.Expressions {
		next, old := pgTrgmOwnerCall(t, node), original[i]
		if next.Ref != reference(old.Ref) || next.Ref.Address == old.Ref.Address || next.Ref.Domain() != tv.Owner().Domain || next.Formal != types(old.Formal) || next.Formal == old.Formal || next.Usage.Type != types(old.Usage.Type) || next.Usage.Type == old.Usage.Type || next.Member != old.Member || next.Source != old.Source || next.Occurrence != old.Occurrence || next.Properties() != old.Properties() {
			t.Fatal("remap lost destination generations, immutable slot or original source occurrence")
		}
		for j, arg := range next.Args {
			if arg.ResultUsage().Type != types(old.Args[j].ResultUsage().Type) {
				t.Fatal("remap lost ordered operand generation")
			}
		}
		current := next.RoutineReference.(*catalog.Procedure)
		if current == old.RoutineReference || current.Address() != te.Installation.Members[next.Member] || current.Revision() != old.RoutineReference.(*catalog.Procedure).Revision() || current.Origin.Installation == old.RoutineReference.(*catalog.Procedure).Origin.Installation || current.Origin.Installation.Members[next.Member] != current.Address() {
			t.Fatal("remap refreshed or retained foreign original instead of reprovenancing its captured declaration")
		}
		if _, _, err := stored.RetagTypes(tv, node, func(d *sqltype.Descriptor) *sqltype.Descriptor { return d }); err != nil {
			t.Fatal("genuine remapped destination recipe rejected", err)
		}
		if selected, ok := node.(plan.SelectedOperatorExpr); ok {
			prior := bound.Expressions[i].(plan.SelectedOperatorExpr)
			if selected.Ref != reference(prior.Ref) || selected.Operator != tv.Get(reference(prior.Ref).Address) || selected.RoutineReference != nil {
				t.Fatal("operator remap lost actual wrapper identity or duplicated admission")
			}
		}
		if !reflect.DeepEqual(pgTrgmOwnerCall(t, bound.Expressions[i]), old) {
			t.Fatal("authorized remap mutated immutable source graph")
		}
	}
	for _, cause := range []string{"membership_order", "source_symbol", "current_signature", "operator_backer", "result_generation", "source_domain"} {
		t.Run(cause, func(t *testing.T) {
			candidate := catalog.CloneState(tv.State())
			mapType, mapRef := types, reference
			switch cause {
			case "membership_order":
				ext := *te
				ext.Members = slices.Clone(te.Members)
				ext.Members[0], ext.Members[1] = ext.Members[1], ext.Members[0]
				candidate.SetDescriptor(ext.Address(), &ext)
			case "source_symbol":
				p := *tv.MustProcedure(te.Installation.Members[1])
				p.Display.Source = "show_limit"
				candidate.SetDescriptor(p.Address(), &p)
			case "current_signature":
				p := *tv.MustProcedure(te.Installation.Members[1])
				p.Signature = "23,25"
				candidate.SetDescriptor(p.Address(), &p)
			case "operator_backer":
				p := *tv.Get(te.Installation.Members[extension.PgTrgmOperatorStart]).(*catalog.Operator)
				p.Function = te.Installation.Members[0].ObjectID
				candidate.SetDescriptor(p.Address(), &p)
			case "result_generation":
				actual, _ := tv.LookupType(1009)
				foreign := *actual
				mapType = func(d *sqltype.Descriptor) *sqltype.Descriptor {
					if d != nil && d.ID() == 1009 {
						return &foreign
					}
					return types(d)
				}
			case "source_domain":
				mapRef = func(ref ids.ObjectRef) ids.ObjectRef {
					if ref == (ids.ObjectRef{}) {
						return ref
					}
					next := reference(ref)
					return sv.Owner().Domain.Ref(next.Epoch, next.Address)
				}
			}
			attempt := stored.ViewQueryRemapper{Catalog: target.Catalog.Read(candidate), Ref: mapRef, Type: mapType, Procedure: procedure}
			_, err := attempt.Nested(bound)
			if cause == "membership_order" {
				if err != nil {
					t.Fatal("current membership order replaced immutable released slot authority", err)
				}
			} else if err == nil {
				t.Fatal("unauthorized source/destination remap accepted")
			}
			if sv.MustExtension(se.Address()) != se || tv.MustExtension(te.Address()) != te {
				t.Fatal("private candidate mutated published source/destination installation")
			}
			for i, node := range bound.Expressions {
				if !reflect.DeepEqual(pgTrgmOwnerCall(t, node), original[i]) {
					t.Fatal("remap candidate changed immutable source occurrence")
				}
			}
		})
	}
}
