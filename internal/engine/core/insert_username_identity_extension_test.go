package core_test

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
)

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
