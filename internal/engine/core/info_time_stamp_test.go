package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func infoTimeStampCatalogType(t *testing.T, view catalog.Read) *catalog.Type {
	t.Helper()
	key := ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}, Name: "time_stamp"}
	addresses := view.Lookup(key)
	if len(addresses) != 1 {
		t.Fatal("selected domain identity", addresses)
	}
	return view.MustType(addresses[0])
}

func TestInfoTimeStampCatalogIdentity(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	d := infoTimeStampCatalogType(t, view)
	base, ok := view.LookupType(sqltype.Timestamptz)
	if !ok || d.SQLType.DomainBase().Type != base || d.SQLType.DomainBase().Typmod != 2 || d.SQLType.ID() == base.ID() || d.ObjectRevision != 1 || !d.SQLType.IsBuiltin() || d.SQLType.IsUserDefined() {
		t.Fatal("builtin catalog authority", d)
	}
	array, ok := view.LookupType(d.Array)
	if !ok || array.Element() != d.SQLType || array.Concrete() || array.ID() != d.SQLType.AssociatedArray() {
		t.Fatal("array identity", array)
	}
	if err := (stored.TypeProvider{}).Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if err := (stored.TypeProvider{}).Validate(view, view.Get(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.ID())})); err != nil {
		t.Fatal(err)
	}
	if err := access.ValidateACL(e.Roles(), d); err != nil {
		t.Fatal("builtin type principal authority", err)
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	relation := domainUnitTable(t, e, op, "CREATE TABLE public.info_time_stamp_unit(v information_schema.time_stamp)")
	current := e.CatalogRead()
	usage := relation.Columns[0].Usage
	if usage.Type != d.SQLType || usage.Typmod != -1 || usage.BaseUsage().Type != base || usage.BaseUsage().Typmod != 2 {
		t.Fatal("logical column usage", usage)
	}
	value, err := usage.Type.Input(context.Background(), "2004-10-19 10:23:54.126+02", -1)
	if err != nil || value.Type() != d.SQLType || sqltype.Null(usage.Type).Type() != d.SQLType {
		t.Fatal("logical value generation", value, err)
	}
	declaration, err := regobj.TypeDeclaration(current, usage)
	if err != nil || declaration != `"information_schema"."time_stamp"` {
		t.Fatal(declaration, err)
	}
	remove := e.BeginCatalogStage(current)
	if err := core.StageRemoveObject(remove, e.Catalog.Domain.Ref(current.Epoch(), d.Address()), false); err == nil {
		t.Fatal("builtin removal admitted")
	}
	if !analyze.StatementCatalogType(d.SQLType) {
		t.Fatal("domain parameter generation fence missing")
	}
}

func TestInfoTimeStampCatalogRemap(t *testing.T) {
	source, target := engine.New(), engine.New()
	original := source.CatalogRead()
	d := infoTimeStampCatalogType(t, original)
	next := catalog.CloneState(original.State())
	baseAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Timestamptz)}
	reboundBase := *next.MustType(baseAddress)
	reboundBase.SQLType = target.CatalogRead().MustType(baseAddress).SQLType
	next.SetDescriptor(baseAddress, &reboundBase)
	// Rendering must use the current captured namespace, not inventory spelling.
	namespace := *next.MustNamespace(d.Namespace())
	next.UnbindName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: namespace.Name()})
	namespace.ObjectName = "renamed_info"
	next.BindName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: namespace.Name()}, namespace.Address())
	next.SetDescriptor(namespace.Address(), &namespace)
	changed, err := typedef.ReownDomainTypes(next, nil, false, routine.RangeTypeRebinder(next, nil))
	if err != nil || !changed {
		t.Fatal(changed, err)
	}
	if err := stored.ReownExpressions(source.Catalog.Read(next), next, nil, true, nil); err != nil {
		t.Fatal(err)
	}
	view := source.Catalog.Read(next)
	rebound := infoTimeStampCatalogType(t, view)
	base, _ := view.LookupType(sqltype.Timestamptz)
	if rebound.SQLType == d.SQLType || rebound.SQLType.DomainBase().Type != base || base == d.SQLType.DomainBase().Type || rebound.SQLType.DomainBase().Typmod != 2 || !rebound.SQLType.IsBuiltin() || rebound.SQLType.IsUserDefined() || rebound.ObjectRevision != 1 || rebound.SQLType.AssociatedArray() != d.Array || rebound.SQLType.NamespaceName() != "information_schema" {
		t.Fatal("reowned builtin provenance", rebound)
	}
	declaration, err := regobj.TypeDeclaration(view, rebound.SQLType.Usage())
	if err != nil || declaration != `"renamed_info"."time_stamp"` || rebound.SQLType.SQLName() != "renamed_info.time_stamp" {
		t.Fatal(declaration, err)
	}
	for _, descriptor := range next.Descriptors() {
		if typ, ok := descriptor.(*catalog.Type); ok {
			if err := (stored.TypeProvider{}).Validate(view, typ); err != nil {
				t.Fatal(typ.Name(), err)
			}
		}
	}
	if d.SQLType.SQLName() != "information_schema.time_stamp" || original.Get(d.Namespace()).Name() != "information_schema" {
		t.Fatal("source generation mutated")
	}
	remapped, _, err := core.EngineRemapPreparedCatalog(target, catalog.CloneState(source.Catalog.State), source.Roles(), target.Roles(), nil)
	if err != nil {
		t.Fatal(err)
	}
	retained := remapped.MustType(d.Address())
	if !retained.SQLType.IsBuiltin() || retained.SQLType.IsUserDefined() {
		t.Fatal("principal remap lost builtin type provenance")
	}
	if err := access.ValidateACL(target.Roles(), retained); err != nil {
		t.Fatal("principal remap left invalid builtin authority", err)
	}
	invalid := catalog.CloneState(original.State())
	if _, err := typedef.ReownDomainTypes(invalid, map[ids.ObjectAddress]ids.ObjectAddress{d.Address(): {ClassID: ids.TypeClassID, ObjectID: 60000}}, false, routine.RangeTypeRebinder(invalid, nil)); err == nil {
		t.Fatal("builtin domain reminted")
	}
}
