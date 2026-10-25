package engine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/role"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDomainIdentityAuthorization(t *testing.T) {
	e := New()
	root, done := roleOperation(t, e, "postgres")
	defer done()
	schemaOwner := roleAttributeCreate(t, e, root, "identity_schema_owner", true, parser.RoleOptions{})
	domainOwner := roleAttributeCreate(t, e, root, "identity_domain_owner", true, parser.RoleOptions{})
	domainUnitPublish(t, e, domainUnitStage(t, e, root, "CREATE SCHEMA owned AUTHORIZATION identity_schema_owner"))
	domainUnitPublish(t, e, domainUnitStage(t, e, root, "CREATE DOMAIN owned.d AS integer"))
	view := e.CatalogRead()
	namespace := view.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "owned"})
	d := view.MustType(view.NameAddress(ids.NameKey{ClassID: ids.TypeClassID, Namespace: namespace, Name: "d"}))
	stage := e.BeginCatalogStage(view)
	if err := role.StageOwnership(stage, d, domainOwner.Ref); err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	actor, closeActor := roleOperation(t, e, schemaOwner.Name)
	defer closeActor()
	e.Mu.Lock()
	_, err := stageSchemaLocked(e, actor, statement(t, "ALTER DOMAIN owned.d RENAME TO denied"), sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
	e.Mu.Unlock()
	domainUnitDiagnostic(t, err, "42501", "must be owner of type owned.d")
	drop := domainUnitStage(t, e, actor, "DROP DOMAIN owned.d")
	defer e.DiscardCatalogStage(drop)
	if len(drop.DomainTargets) != 1 || drop.DomainTargets[0] != d.Address() {
		t.Fatal("schema owner did not authorize exact target", drop.DomainTargets)
	}
	live := e.CatalogRead().MustType(d.Address())
	array := e.CatalogRead().MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(d.Array)})
	if live.Owner != domainOwner.Ref || array.Owner != domainOwner.Ref || live.SQLType != d.SQLType || d.Owner == live.Owner {
		t.Fatal("owner recursion or immutable source changed")
	}
}

func TestDomainIdentityPlanStamp(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	replacementOwner := roleAttributeCreate(t, e, op, "identity_stamp_owner", true, parser.RoleOptions{}).Ref
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.d AS integer CONSTRAINT positive CHECK(VALUE>0)"))
	old := domainUnitType(t, e.CatalogRead(), "d")
	stamp := core.PreparedDefinitionStamp(old, op.Session)
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "ALTER DOMAIN public.d RENAME TO renamed"))
	current := e.CatalogRead().MustType(old.Address())
	if current.Revision() == old.Revision() || core.PreparedDefinitionStamp(current, op.Session) != stamp {
		t.Fatal("identity edit invalidated executable stamp")
	}
	// Reinstalling another generation at that SAME staged revision must not
	// normalize against the older committed revision and lose preservation.
	stage := e.BeginCatalogStage(e.CatalogRead())
	next := *current
	next.ObjectRevision++
	if err := stage.ReplaceDescriptor(&next, false, true); err != nil {
		t.Fatal(err)
	}
	refresh := *stage.Get(next.Address()).(*catalog.Type)
	if err := stage.Replace(&refresh); err != nil {
		t.Fatal(err)
	}
	if core.PreparedDefinitionStamp(stage.Get(next.Address()), op.Session) != stamp {
		t.Fatal("same-revision refresh lost normalized stamp")
	}
	e.DiscardCatalogStage(stage)
	// A changed CHECK recipe with unchanged SQLType flags is still a genuine
	// semantic mutation. Ordinary replacement is invalidating by default.
	stage = e.BeginCatalogStage(e.CatalogRead())
	changed := *current
	changed.ObjectRevision++
	changed.DomainChecks = append([]catalog.DomainCheckConstraint(nil), current.DomainChecks...)
	changed.DomainChecks[0].NotValid = !changed.DomainChecks[0].NotValid
	if err := stage.Replace(&changed); err != nil {
		t.Fatal(err)
	}
	if core.PreparedDefinitionStamp(stage.Get(changed.Address()), op.Session) == stamp || stage.Get(changed.Address()).(*catalog.Type).DomainPlanStamp != 0 {
		t.Fatal("CHECK change reused identity-only stamp")
	}
	e.DiscardCatalogStage(stage)
	for _, mutate := range []func(*catalog.Type){
		func(d *catalog.Type) { d.Owner = replacementOwner },
		func(d *catalog.Type) { d.ACLSyntheticDefault = false },
	} {
		next := *current
		next.ObjectRevision++
		mutate(&next)
		normalized := catalog.NormalizeTypeReplacement(current, &next, false).(*catalog.Type)
		if normalized.EffectiveDomainPlanStamp() == stamp || current.EffectiveDomainPlanStamp() != stamp {
			t.Fatal("ordinary authority replacement retained stamp or mutated source")
		}
	}
}

func TestDomainIdentityArrayPlanStamp(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	replacementOwner := roleAttributeCreate(t, e, op, "identity_array_stamp_owner", true, parser.RoleOptions{}).Ref
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE DOMAIN public.d AS integer CONSTRAINT positive CHECK(VALUE>0)"))
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "CREATE SCHEMA identity_dst"))
	old := domainUnitType(t, e.CatalogRead(), "d")
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(old.Array)}
	oldArray := e.CatalogRead().MustType(address)
	stamp := core.PreparedDefinitionStamp(oldArray, op.Session)
	elementStamp := core.PreparedDefinitionStamp(old, op.Session)
	// This rename first displaces the domain's OWN array. Both certified
	// identity boundaries must preserve the same original effective stamp.
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "ALTER DOMAIN public.d RENAME TO _d"))
	domainUnitPublish(t, e, domainUnitStage(t, e, op, "ALTER DOMAIN public._d SET SCHEMA identity_dst"))
	current := e.CatalogRead().MustType(address)
	if current.Revision() == oldArray.Revision() || core.PreparedDefinitionStamp(current, op.Session) != stamp || oldArray.EffectiveDomainPlanStamp() != stamp {
		t.Fatal("companion identity edit changed semantic stamp or retained root")
	}
	stage := e.BeginCatalogStage(e.CatalogRead())
	next := *current
	next.ObjectRevision++
	if err := stage.ReplaceDescriptor(&next, false, true); err != nil {
		t.Fatal(err)
	}
	refresh := *stage.Get(address).(*catalog.Type)
	if err := stage.Replace(&refresh); err != nil {
		t.Fatal(err)
	}
	if core.PreparedDefinitionStamp(stage.Get(address), op.Session) != stamp {
		t.Fatal("companion generation refresh lost identity preservation")
	}
	e.DiscardCatalogStage(stage)
	for _, mutate := range []func(*catalog.Type){
		func(d *catalog.Type) { d.Owner = replacementOwner },
		func(d *catalog.Type) { d.ACLSyntheticDefault = false },
	} {
		next := *current
		next.ObjectRevision++
		mutate(&next)
		normalized := catalog.NormalizeTypeReplacement(current, &next, false).(*catalog.Type)
		if normalized.DomainPlanStamp != 0 || normalized.EffectiveDomainPlanStamp() == stamp {
			t.Fatal("ordinary companion authority replacement retained identity-only stamp")
		}
	}
	usingIndexUnitExecute(t, e, sessionStart(t, e, nil), statement(t, "ALTER DOMAIN identity_dst._d ADD CONSTRAINT upper_bound CHECK(VALUE<10)"))
	if core.PreparedDefinitionStamp(e.CatalogRead().MustType(old.Address()), op.Session) == elementStamp {
		t.Fatal("element CHECK change kept the captured semantic dependency valid")
	}
}
