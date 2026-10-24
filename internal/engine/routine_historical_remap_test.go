package engine

import (
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

func TestRoutineHistoricalBuiltinReplacementRemap(t *testing.T) {
	source, target := New(), New()
	originals := make(map[string]*catalog.Procedure)
	for _, descriptor := range source.catalog.State.Descriptors() {
		if p, ok := descriptor.(*catalog.Procedure); ok && (p.Name() == "bool_and" || p.Name() == "every" || p.Name() == "row_number") {
			originals[p.Name()] = p
		}
	}
	for _, name := range []string{"bool_and", "every", "row_number"} {
		if originals[name] == nil {
			t.Fatal("missing seeded declaration", name)
		}
	}
	boolean, _ := source.CatalogRead().LookupType(sqltype.Bool)
	integer, _ := source.CatalogRead().LookupType(sqltype.Int8)
	ref := func(p *catalog.Procedure) ids.ObjectRef {
		return source.catalog.Domain.Ref(source.catalog.State.Epoch(), p.Address())
	}
	aggregate := &plan.AggregateNode{Child: &plan.SingletonNode{}, Calls: []plan.AggregateCall{{
		Provider: functions.NewBooleanAggregateProvider(boolean.Usage(), true),
		Routine:  ref(originals["bool_and"]), Procedure: originals["bool_and"],
	}, {
		Provider: functions.NewBooleanAggregateProvider(boolean.Usage(), true),
		Routine: ref(originals["every"]), Procedure: originals["every"],
	}}}
	window := &plan.WindowNode{Child: aggregate, Calls: []plan.WindowCall{{Routine: ref(originals["row_number"]), Procedure: originals["row_number"], Expression: &plan.WindowExpr{Info: expr.Info{Usage: integer.Usage()}}}}}
	candidate := catalog.CloneState(source.catalog.State)
	for _, original := range originals {
		current := *original
		current.ObjectName = "current_" + original.Name()
		current.ObjectRevision++
		candidate.SetDescriptor(current.Address(), &current)
	}
	sourceRoles := source.roles.PublishedRead(&source.catalog.Owner, source.CatalogRead())
	targetRoles := target.roles.PublishedRead(&target.catalog.Owner, target.CatalogRead())
	structural, err := stored.RemapViewQuery(target.CatalogRead(), aggregate, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
	if err != nil {
		t.Fatal(err)
	}
	unproven := structural.(*plan.AggregateNode).Calls[0]
	auth := exec.RoutineAccess{View: access.View{Catalog: target.CatalogRead(), Roles: targetRoles}}
	checks := []catalog.ExpressionDependency{{Ref: unproven.Routine, Revision: unproven.Procedure.Revision()}}
	if unproven.Procedure != originals["bool_and"] || exec.AdmitRoutineDescriptor(auth, checks, unproven.Procedure, nil) == nil {
		t.Fatal("structural type copying acquired foreign routine authority")
	}
	target.mu.Lock()
	prepared, procedure, err := target.remapPreparedCatalog(candidate, sourceRoles, targetRoles, nil)
	target.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	view := target.catalog.Read(prepared)
	mapper := &stored.ViewQueryRemapper{Catalog: view, Procedure: procedure, Ref: func(old ids.ObjectRef) ids.ObjectRef {
		return target.catalog.Domain.Ref(view.Epoch(), old.Address)
	}, Type: func(old *sqltype.Descriptor) *sqltype.Descriptor { return old }}
	mapped, err := mapper.Query(window)
	if err != nil {
		t.Fatal(err)
	}
	mappedWindow := mapped.(*plan.WindowNode)
	mappedAggregate := mappedWindow.Child.(*plan.AggregateNode)
	captured := []*catalog.Procedure{mappedAggregate.Calls[0].Procedure, mappedAggregate.Calls[1].Procedure, mappedWindow.Calls[0].Procedure}
	for i, name := range []string{"bool_and", "every", "row_number"} {
		old, got := originals[name], captured[i]
		current, _ := view.Procedure(old.Address())
		if got == old || got == current || got.Name() != old.Name() || got.Revision() != old.Revision() || got.Owner != target.roles.Domain.Principal(old.Owner.ID()) || current.Revision() == got.Revision() {
			t.Fatal("historical carrier replaced rather than reprovenanced", name, got, current)
		}
		if old.Owner != source.roles.Domain.Principal(old.Owner.ID()) || old.Name() != name {
			t.Fatal("source original mutated", name)
		}
	}
	dependencies := make([]catalog.ExpressionDependency, 0, len(originals))
	for _, name := range []string{"bool_and", "every", "row_number"} {
		dependencies = append(dependencies, catalog.ExpressionDependency{Ref: ref(originals[name]), Revision: originals[name].Revision()})
	}
	frozen := stored.NewView(stored.ViewParts{Query: &plan.Query{Root: window}, Dependencies: dependencies})
	rebound, err := stored.RemapView(view, frozen, nil, mapper.Ref, mapper.Type, nil, procedure)
	if err != nil {
		t.Fatal(err)
	}
	for i, dependency := range rebound.Dependencies() {
		if dependency.Revision != dependencies[i].Revision || dependency.Ref.Domain() != target.catalog.Domain || frozen.Dependencies()[i] != dependencies[i] {
			t.Fatal("raw remap refreshed a historical manifest or changed its source", dependency)
		}
	}
	if mappedAggregate.Calls[1].Routine.Address != captured[1].Address() || mappedWindow.Calls[0].Routine.Address != captured[2].Address() {
		t.Fatal("aggregate/window references separated from their original proofs")
	}
	forged := *originals["every"]
	forged.Owner = target.roles.Domain.Principal(forged.Owner.ID())
	if _, err := procedure(&forged); err == nil {
		t.Fatal("target-domain owner rescued an imported-source carrier")
	}
	if aggregate.Calls[1].Procedure != originals["every"] || window.Calls[0].Procedure != originals["row_number"] {
		t.Fatal("query remap mutated the source captures")
	}
}

func TestRoutineHistoricalTemporaryPrincipalRemap(t *testing.T) {
	e := New()
	var original *catalog.Procedure
	for _, descriptor := range e.catalog.State.Descriptors() {
		if p, ok := descriptor.(*catalog.Procedure); ok && p.Name() == "row_number" {
			original = p
			break
		}
	}
	if original == nil {
		t.Fatal("missing seeded window declaration")
	}
	// Retired owners/grantees are provenance, not the current EXECUTE policy.
	owner := e.roles.Domain.Principal(900001)
	grantee := e.roles.Domain.Principal(900002)
	oldOwner := authorization.RoleDescriptor{Ref: owner, Name: "historical_owner"}
	oldGrantee := authorization.RoleDescriptor{Ref: grantee, Name: "historical_reader"}
	roleRoot, err := e.roles.Published.Root.PutDescriptor(oldOwner)
	if err != nil {
		t.Fatal(err)
	}
	roleRoot, err = roleRoot.PutDescriptor(oldGrantee)
	if err != nil {
		t.Fatal(err)
	}
	roleRoot = roleRoot.Retire(map[authorization.PrincipalRef]authorization.RoleDescriptor{owner: oldOwner, grantee: oldGrantee})
	e.roles.PublishLocked(&access.RoleRecord{Root: roleRoot, Revision: e.roles.Published.Revision + 1})
	historical := *original
	historical.Owner = owner
	historical.ACL = authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: grantee}, Privileges: authorization.ExecutePrivilege}}}
	current := *original
	current.ObjectRevision++
	e.catalog.State.SetDescriptor(current.Address(), &current)
	roles := e.roles.PublishedRead(&e.catalog.Owner, e.CatalogRead())
	if _, err := roles.Descriptor(owner); err == nil {
		t.Fatal("historical-owner control unexpectedly live")
	}
	mapper := e.retainedRoutineProcedureRemapper(e.CatalogRead())
	mapped, err := mapper(&historical)
	if err != nil {
		t.Fatal("retained target-domain historical metadata rejected", err)
	}
	if mapped == &historical || mapped == &current || mapped.Revision() != historical.Revision() || mapped.Owner != owner || mapped.ACL.Entries[0].Grantee.Role != grantee || historical.Owner != owner {
		t.Fatal("TEMP historical principal identity or revision replaced")
	}
	foreign := new(authorization.Domain)
	badOwner := historical
	badOwner.Owner = foreign.Principal(owner.ID())
	if _, err := mapper(&badOwner); err == nil {
		t.Fatal("foreign TEMP owner accepted by numeric identity")
	}
	badACL := historical
	badACL.ACL.Entries = append([]authorization.ACLEntry(nil), historical.ACL.Entries...)
	badACL.ACL.Entries[0].Grantee.Role = foreign.Principal(grantee.ID())
	if _, err := mapper(&badACL); err == nil {
		t.Fatal("mixed-domain TEMP ACL accepted")
	}
}

// replacementProcedurePolicyFixture supplies the real host policy for private
// address/type remap fixtures; it does not manufacture numeric principal links.
func replacementProcedurePolicyFixture(source, target *Engine) catalog.ProcedureRemapper {
	sourceRoles := source.roles.PublishedRead(&source.catalog.Owner, source.catalog.Current())
	targetRoles := target.roles.PublishedRead(&target.catalog.Owner, target.catalog.Current())
	return catalog.NewProcedureRemapper(target.roles.PrincipalRemap(&target.catalog, sourceRoles, targetRoles, nil, routine.OperatorSeeded))
}
