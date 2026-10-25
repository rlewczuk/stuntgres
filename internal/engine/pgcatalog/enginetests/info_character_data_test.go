package enginetests_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func infoCharacterDataUnitCatalog(t *testing.T, e *core.Engine, view catalog.Read) *catalog.Type {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13246}
	d, ok := view.Type(address)
	if !ok || d.Name() != "character_data" || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}) || d.ObjectRevision == 0 || !d.SQLType.IsBuiltin() || !d.SQLType.Concrete() || !d.SQLType.IsDomain() || d.SQLType.HasDomainConstraints() || len(d.DomainChecks) != 0 || d.DomainNotNull != nil || d.DomainDefault != nil || d.DomainDefaultOwn || d.DomainDefaultText != "" {
		t.Fatal("unconstrained builtin domain wrapper", d)
	}
	base, ok := view.LookupType(sqltype.VarChar)
	physical := sqltype.Usage{Type: base, Typmod: -1, Collation: sqltype.CollationRef{ID: 950, Derivation: sqltype.Default}}
	if !ok || d.SQLType.DomainBase() != physical || d.SQLType.Usage().BaseUsage() != physical {
		t.Fatal("canonical immediate base generation", d.SQLType.DomainBase())
	}
	namespace, ok := view.Descriptor(d.Namespace())
	if !ok || namespace.Name() != "information_schema" {
		t.Fatal("selected namespace", namespace)
	}
	array, ok := view.LookupType(13245)
	if !ok || array.Element() != d.SQLType || array.Concrete() || array.Usage().Valid() || d.Array != 13245 {
		t.Fatal("lookup-only companion backlink", array)
	}
	if err := e.Catalog.Providers[ids.TypeClassID].Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if err := catalog.ValidateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestInfoCharacterDataBuiltinCatalog(t *testing.T) {
	source := engine.New()
	before := source.CatalogRead()
	original := infoCharacterDataUnitCatalog(t, source, before)
	originalBase := original.SQLType.DomainBase()
	target := engine.New()
	if _, err := target.ReplaceCatalog(context.Background(), source, before); err != nil {
		t.Fatal(err)
	}
	view := target.CatalogRead()
	mapped := infoCharacterDataUnitCatalog(t, target, view)
	if mapped == original {
		t.Fatal("replacement retained source catalog wrapper")
	}
	if source.CatalogRead().State() != before.State() || original.SQLType.DomainBase() != originalBase {
		t.Fatal("replacement mutated source authority")
	}
	infoCharacterDataUnitCatalog(t, source, before)
}

func TestInfoCharacterDataBuiltinBaseRebind(t *testing.T) {
	source, fresh := engine.New(), engine.New()
	originalView := source.CatalogRead()
	original := infoCharacterDataUnitCatalog(t, source, originalView)
	originalBase := original.SQLType.DomainBase()
	next := catalog.CloneState(originalView.State())
	baseAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.VarChar)}
	base := *next.MustType(baseAddress)
	base.SQLType, _ = fresh.CatalogRead().LookupType(sqltype.VarChar)
	next.SetDescriptor(baseAddress, &base)
	changed, err := typedef.ReownDomainTypes(next, nil, false, routine.RangeTypeRebinder(next, nil))
	if err != nil || !changed {
		t.Fatal("actual base-generation replacement did not rebind", changed, err)
	}
	if err := stored.ReownExpressions(source.Catalog.Read(next), next, nil, true, nil); err != nil {
		t.Fatal(err)
	}
	mapped := infoCharacterDataUnitCatalog(t, source, source.Catalog.Read(next))
	if mapped == original || mapped.SQLType == original.SQLType || mapped.SQLType.DomainBase().Type != base.SQLType || mapped.SQLType.DomainBase().Type == originalBase.Type {
		t.Fatal("changed base did not rebuild domain authority")
	}
	if source.CatalogRead().State() != originalView.State() || original.SQLType.DomainBase() != originalBase {
		t.Fatal("private rebind mutated published source")
	}
	infoCharacterDataUnitCatalog(t, source, originalView)
}

func TestInfoCharacterDataLogicalDescriptions(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	table := domainUnitTable(t, e, op, "CREATE TABLE public.character_data_description(v information_schema.character_data)")
	view := e.CatalogRead()
	d, ok := view.LookupType(13246)
	if !ok || table.Columns[0].Usage != d.Usage() {
		t.Fatal("declaration lost logical domain", table.Columns)
	}
	check := func(query string) {
		t.Helper()
		bound := domainUnitBindSelect(t, e, op, query)
		schema := bound.Root.Schema()
		if len(schema) != 1 || schema[0].Usage != d.Usage() || schema[0].Usage.BaseUsage() != d.DomainBase() || schema[0].Usage.BaseUsage().Type.ID() != sqltype.VarChar || schema[0].Usage.Typmod != -1 || schema[0].Usage.Collation != (sqltype.CollationRef{ID: 950, Derivation: sqltype.Default}) {
			t.Fatal("logical result description lost domain/base/C authority", schema)
		}
	}
	check("SELECT v FROM public.character_data_description WHERE FALSE")
	// Private storage fixture: binding must retain the logical nullable domain
	// even when every stored physical datum is NULL.
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(table.Address(), &storage.Relation{Generation: 1, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{sqltype.Null(d)}}}})
	e.Visibility.SetCommitSeqLocked(1)
	e.Mu.Unlock()
	check("SELECT v FROM public.character_data_description")
	check("SELECT NULL::information_schema.character_data AS v")
}
