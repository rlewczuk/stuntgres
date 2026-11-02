package schemacmds_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
)

func earthCheckDependencyWitness(t *testing.T, view catalog.Read, domain *catalog.Type) {
	t.Helper()
	if err := stored.ValidateDomainCheckDefinitions(view, domain); err != nil {
		t.Fatal("CHECK provider definitions", err)
	}
	edges, err := pgcatalog.CollectDepend(context.Background(), view, core.NewCatalogRuntime(core.CatalogRuntimeFields{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range domain.DomainChecks {
		node := check.Expression.(plan.EarthCheckExpr)
		captured, err := exec.ExpressionRoutineDescriptor(node)
		if err != nil || captured == nil {
			t.Fatal("CHECK lost immutable helper capture", check.Name, err)
		}
		want := map[string]string{"not_point": "cube_is_point", "not_3d": "cube_dim", "on_surface": "cube_distance"}[check.Name]
		if symbol, err := extension.ProcedureSymbol(view, view.MustProcedure(captured.Address())); err != nil || symbol != want {
			t.Fatal("CHECK helper role", check.Name, symbol, err)
		}
		base := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(node.Value.Usage.Type.ID())})
		helper, err := extension.EarthCheckHelper(view, base, node.Kind)
		if err != nil || helper.Address() != captured.Address() {
			t.Fatal("CHECK helper left genuine cube cohort", check.Name, err)
		}
		found := false
		for _, dependency := range check.Dependencies {
			if dependency.Ref.Address == captured.Address() && dependency.Ref.Domain() == view.Owner().Domain && dependency.Ref.Epoch == view.Epoch() && dependency.Revision == captured.Revision() && dependency.Purpose == catalog.DefinitionDependency {
				found = true
			}
		}
		if !found {
			t.Fatal("CHECK has no owning helper revision/reference", check.Name, check.Dependencies)
		}
		refs, err := stored.ExpressionDependRefs(context.Background(), view, node)
		if err != nil || !slices.Contains(refs, captured.Address()) {
			t.Fatal("ordinary expression visitor lost helper", check.Name, refs, err)
		}
		projected := false
		for _, edge := range edges {
			if edge.Dependent == (ids.ObjectAddress{ClassID: 2606, ObjectID: check.OID}) && edge.Referenced == captured.Address() && edge.DepType == 'n' {
				projected = true
			}
		}
		if !projected {
			t.Fatal("ordinary CHECK projection lost normal helper edge", check.Name)
		}
	}
}

func TestExtensionDependencyEarthCheckReferences(t *testing.T) {
	e, _, domain := earthUnitInstalled(t)
	view := e.CatalogRead()
	earthCheckDependencyWitness(t, view, domain)
	point := domain.DomainChecks[1].Expression.(plan.EarthCheckExpr)
	captured := point.RoutineReference.(*catalog.Procedure)
	for _, reason := range []string{"missing_capture", "wrong_role", "foreign_capture", "missing_helper", "foreign_helper", "copied_origin", "wrong_slot", "stale_capture_revision", "stale_manifest_revision", "missing_manifest", "foreign_manifest", "foreign_check_manifest", "coherent_foreign_manifest", "coherent_future_epoch_manifest", "mixed_manifest_epoch", "zero_manifest_epoch"} {
		t.Run(reason, func(t *testing.T) {
			state := catalog.CloneState(view.State())
			bad := *domain
			bad.DomainChecks = slices.Clone(domain.DomainChecks)
			for i := range bad.DomainChecks {
				bad.DomainChecks[i].Dependencies = slices.Clone(domain.DomainChecks[i].Dependencies)
			}
			check := &bad.DomainChecks[1]
			node := point
			switch reason {
			case "missing_capture":
				node.RoutineReference = nil
			case "wrong_role":
				node.RoutineReference = domain.DomainChecks[0].Expression.(plan.EarthCheckExpr).RoutineReference
			case "foreign_capture":
				copy := *captured
				copy.Owner = (&authorization.Domain{}).Principal(copy.Owner.ID())
				node.RoutineReference = &copy
			case "missing_helper":
				state.RemoveDescriptor(captured.Address())
			case "foreign_helper":
				copy := *captured
				copy.Owner = (&authorization.Domain{}).Principal(copy.Owner.ID())
				state.SetDescriptor(copy.Address(), &copy)
			case "copied_origin":
				copy := *captured
				origin := *copy.Origin
				installation := *origin.Installation
				origin.Installation = &installation
				copy.Origin = &origin
				state.SetDescriptor(copy.Address(), &copy)
			case "wrong_slot":
				copy := *captured
				origin := *copy.Origin
				origin.Declaration++
				copy.Origin = &origin
				state.SetDescriptor(copy.Address(), &copy)
			case "stale_capture_revision":
				copy := *captured
				copy.ObjectRevision++
				node.RoutineReference = &copy
			case "stale_manifest_revision":
				for i := range check.Dependencies {
					if check.Dependencies[i].Ref.Address == captured.Address() {
						check.Dependencies[i].Revision++
					}
				}
			case "missing_manifest":
				check.Dependencies = nil
			case "foreign_manifest":
				for i := range check.Dependencies {
					if check.Dependencies[i].Ref.Address == captured.Address() {
						check.Dependencies[i].Ref = (&ids.Domain{}).Ref(view.Epoch(), captured.Address())
					}
				}
			case "foreign_check_manifest":
				foreign := &ids.Domain{}
				for i := range check.Dependencies {
					check.Dependencies[i].Ref = foreign.Ref(view.Epoch(), check.Dependencies[i].Ref.Address)
				}
			case "coherent_foreign_manifest":
				foreign := &ids.Domain{}
				for i := range bad.DomainChecks {
					for j := range bad.DomainChecks[i].Dependencies {
						dependency := &bad.DomainChecks[i].Dependencies[j]
						dependency.Ref = foreign.Ref(view.Epoch(), dependency.Ref.Address)
					}
				}
			case "coherent_future_epoch_manifest":
				for i := range bad.DomainChecks {
					for j := range bad.DomainChecks[i].Dependencies {
						dependency := &bad.DomainChecks[i].Dependencies[j]
						dependency.Ref = view.Owner().Domain.Ref(view.Epoch()+1, dependency.Ref.Address)
					}
				}
			case "mixed_manifest_epoch", "zero_manifest_epoch":
				for i := range check.Dependencies {
					if check.Dependencies[i].Ref.Address == captured.Address() {
						epoch := view.Epoch() + 1
						if reason == "zero_manifest_epoch" {
							epoch = 0
						}
						check.Dependencies[i].Ref = view.Owner().Domain.Ref(epoch, captured.Address())
					}
				}
			}
			check.Expression = node
			state.SetDescriptor(bad.Address(), &bad)
			read := view.WithState(state)
			if err := stored.ValidateDomainCheckDefinitions(read, &bad); err == nil {
				t.Fatal("invalid CHECK helper authority/manifest accepted", reason)
			}
			remap := stored.NewRoutineRemap(read, nil, nil, nil, false)
			if err := stored.ReownInstalledDomainChecks(read, state, nil, false, remap); err == nil {
				t.Fatal("bootstrap laundered invalid helper or source revision", reason)
			}
			if state.Get(bad.Address()) != &bad {
				t.Fatal("failed bootstrap replaced invalid source")
			}
		})
	}
	t.Run("current_labels_and_metadata_do_not_replace_source_capture", func(t *testing.T) {
		state := catalog.CloneState(view.State())
		renamed := *captured
		renamed.ObjectName = "actual_point_helper"
		renamed.ObjectRevision++
		state.UnbindName(catalog.DescriptorNameKey(captured))
		state.SetDescriptor(renamed.Address(), &renamed)
		state.BindName(catalog.DescriptorNameKey(&renamed), renamed.Address())
		read := view.WithState(state)
		if err := stored.ValidateDomainCheckDefinitions(read, domain); err != nil {
			t.Fatal("legitimate metadata replacement invalidated source declaration", err)
		}
		base := domain.SQLType.DomainBase()
		text, err := (stored.ScalarRenderer{DomainValue: &base, CapturedChecks: domain.DomainChecks[1].Dependencies}).SQL(context.Background(), read, point)
		if err != nil || !strings.Contains(text, "actual_point_helper(VALUE)") {
			t.Fatal("renderer did not use authenticated current label", text, err)
		}
		if point.RoutineReference != captured || captured.Name() != "cube_is_point" {
			t.Fatal("current presentation mutated original helper capture")
		}
	})
}
