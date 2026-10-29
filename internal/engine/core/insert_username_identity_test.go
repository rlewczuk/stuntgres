package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

func TestInsertUsernameProviderIdentity(t *testing.T) {
	e, installed := ownershipUnitInstall(t, "insert_username")
	view := e.CatalogRead()
	if len(installed.Members) != 1 { t.Fatal("released member inventory", installed.Members) }
	d := view.MustProcedure(installed.Members[0])
	if d.Builtin != nil || d.SQL != nil || !catalog.SupportsDMLTrigger(d) { t.Fatal("installed native capability", d) }
	if err := (stored.ProcedureProvider{}).Validate(view, d); err != nil { t.Fatal(err) }
	if symbol, err := extension.ProcedureSymbol(view, d); err != nil || symbol != "insert_username" { t.Fatal("genuine symbol", symbol, err) }
	for _, change := range []struct { name string; apply func(*catalog.Procedure) }{
		{"implementation", func(p *catalog.Procedure) { p.Implementation = nil }},
		{"copied_implementation", func(p *catalog.Procedure) { p.Implementation = new(extension.InsertUsernameProvider) }},
		{"display", func(p *catalog.Procedure) { p.Display.Source = "forged" }},
		{"declaration", func(p *catalog.Procedure) { p.Installed.Parallel = 's' }},
		{"signature", func(p *catalog.Procedure) { p.Signature = "25" }},
		{"result", func(p *catalog.Procedure) { p.Result = 25 }},
		{"kind", func(p *catalog.Procedure) { p.RoutineKind = catalog.ProcedureRoutine }},
		{"origin", func(p *catalog.Procedure) { p.Origin = nil }},
		{"slot", func(p *catalog.Procedure) { origin := *p.Origin; origin.Declaration++; p.Origin = &origin }},
		{"unsealed", func(p *catalog.Procedure) { origin := *p.Origin; recipe := *origin.Installation; origin.Installation = &recipe; p.Origin = &origin }},
	} {
		t.Run(change.name, func(t *testing.T) {
			bad := *d; change.apply(&bad)
			state := catalog.CloneState(view.State()); state.SetDescriptor(d.Address(), &bad)
			if err := (stored.ProcedureProvider{}).Validate(e.Catalog.Read(state), &bad); err == nil { t.Fatal("forged provider admitted") }
		})
	}
	foreign, foreignExt := ownershipUnitInstall(t, "insert_username")
	other := foreign.CatalogRead().MustProcedure(foreignExt.Members[0])
	if err := (stored.ProcedureProvider{}).Validate(view, other); err == nil { t.Fatal("foreign provider admitted") }
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE ROLE iu_private_owner", "CREATE SCHEMA iu_private", "ALTER FUNCTION insert_username() OWNER TO iu_private_owner", "ALTER FUNCTION insert_username() SECURITY DEFINER", "ALTER FUNCTION insert_username() SET search_path=pg_catalog", "ALTER FUNCTION insert_username() RENAME TO iu_private_real", "ALTER EXTENSION insert_username SET SCHEMA iu_private", "CREATE FUNCTION public.insert_username() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$"} { enginetest.RoleExecute(t, e, op, q) }
	current := e.CatalogRead()
	changed := current.MustProcedure(d.Address())
	if changed.Origin != d.Origin || changed.Installed != d.Installed || changed.Implementation != d.Implementation { t.Fatal("mutable attributes replaced immutable authority") }
	if err := (stored.ProcedureProvider{}).Validate(current, changed); err != nil { t.Fatal("authentic changed provider", err) }
	if err := (stored.ProcedureProvider{}).Validate(current, d); err == nil { t.Fatal("noncurrent provider admitted") }
	decoy := current.MustProcedure(current.NameAddress(ids.NameKey{ClassID: ids.ProcedureClassID, Namespace: d.Namespace(), Name: "insert_username"}))
	if decoy.Implementation == d.Implementation || decoy.Origin != nil || decoy.SQL == nil { t.Fatal("name impersonation acquired native provider") }
}

func TestInsertUsernameControls(t *testing.T) {
	e, ext := ownershipUnitInstall(t, "insert_username")
	view := e.CatalogRead()
	recipe := ext.Installation
	if recipe == nil || !recipe.Valid() || len(recipe.Members) != 1 { t.Fatal("missing released recipe") }
	a := recipe.Members[0]
	mapping := map[ids.ObjectAddress]ids.ObjectAddress{
		ext.Address(): {ClassID: ids.ExtensionClassID, ObjectID: 930000},
		ext.TargetNamespace: {ClassID: ids.NamespaceClassID, ObjectID: 930001},
		a: {ClassID: ids.ProcedureClassID, ObjectID: 930002},
	}
	remap := extension.DescriptorRemapper(mapping)
	mapped := remap(ext).(*catalog.Extension)
	member := remap(view.MustProcedure(a)).(*catalog.Procedure)
	if mapped.Installation == recipe || !mapped.Installation.Valid() || member.Origin.Installation != mapped.Installation || !member.Origin.Valid(mapping[a]) { t.Fatal("remap did not share genuine destination graph") }
	canonical := mapped.Installation.Declarations[0].(*catalog.Procedure)
	if canonical.Address() != mapping[a] || canonical.Namespace() != mapping[ext.TargetNamespace] || canonical.Origin != nil || canonical.Implementation != member.Implementation { t.Fatal("canonical remap identity") }
	if recipe.Members[0] != a || recipe.Declarations[0].Address() != a || view.MustProcedure(a).Origin.Installation != recipe { t.Fatal("source authority mutated") }
	if extension.DescriptorRemapper(nil)(ext) != ext { t.Fatal("identity remap copied graph") }
	forged := *recipe
	if forged.Valid() { t.Fatal("copied installation forged seal") }
}
