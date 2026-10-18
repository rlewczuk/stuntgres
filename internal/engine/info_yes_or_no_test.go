package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func infoYesOrNoUnitCatalog(t *testing.T, e *Engine, view CatalogRead) *typeDescriptor {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13256}
	d, ok := view.Type(address)
	if !ok || d.Name() != "yes_or_no" || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}) || d.ObjectRevision == 0 || !d.SQLType.IsBuiltin() || !d.SQLType.Concrete() || !d.SQLType.HasDomainChecks() {
		t.Fatal("selected domain wrapper", d)
	}
	base, ok := view.LookupType(sqltype.VarChar)
	if !ok || d.SQLType.DomainBase() != (sqltype.Usage{Type: base, Typmod: 7, Collation: sqltype.CollationRef{ID: 950, Derivation: sqltype.Default}}) {
		t.Fatal("selected immediate base generation", d.SQLType.DomainBase())
	}
	namespace, ok := view.Descriptor(d.Namespace())
	if !ok || namespace.Name() != "information_schema" {
		t.Fatal("selected namespace", namespace)
	}
	array, ok := view.LookupType(13255)
	if !ok || array.Element() != d.SQLType || array.Concrete() || d.Array != 13255 {
		t.Fatal("selected companion backlink", array)
	}
	if len(d.DomainChecks) != 1 {
		t.Fatal("own recipe count", len(d.DomainChecks))
	}
	check := d.DomainChecks[0]
	if check.OID == 0 || check.Name != "yes_or_no_check" || check.NotValid || check.Ordinal != 0 || check.Expression == nil || len(check.Dependencies) == 0 || !check.Expression.Properties().DomainDependent || check.Expression.Properties().RowDependent {
		t.Fatal("real validated VALUE recipe", check)
	}
	for _, dependency := range check.Dependencies {
		provider, ok := view.Descriptor(dependency.Ref.Address)
		if !ok || dependency.Ref.Domain() != e.catalog.Domain || dependency.Ref.Epoch != view.Epoch() || dependency.Revision != provider.Revision() {
			t.Fatal("captured recipe dependency", dependency)
		}
	}
	valueLeaves := 0
	walkBoundExpression(check.Expression, func(node expr.Expr) {
		if _, ok := node.(domainValueExpression); ok {
			valueLeaves++
			if node.ResultUsage() != d.SQLType.DomainBase() {
				t.Fatal("VALUE lost immediate base/typmod/C authority", node.ResultUsage())
			}
		}
	})
	if valueLeaves == 0 {
		t.Fatal("recipe does not consume real VALUE")
	}
	if err := e.catalog.Providers[ids.TypeClassID].Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if err := validateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
	return d
}

func infoYesOrNoUnitEvaluate(t *testing.T, view CatalogRead, d *typeDescriptor) {
	t.Helper()
	base := d.SQLType.DomainBase().Type
	ctx := context.Background()
	eval := EvalContext{Context: ctx, catalog: view}
	eval.Input.DomainChecks = &eval
	plan, err := sqltype.PlanCoercion(base.Usage(), d.SQLType.Usage(), sqltype.Assignment)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []sqltype.Value{sqltype.String(base, "YES"), sqltype.String(base, "NO"), sqltype.Null(base)} {
		out, err := plan.Apply(ctx, value, eval.Input, eval.Output)
		if err != nil || out.Type() != d.SQLType || out.IsNull() != value.IsNull() {
			t.Fatal("real TRUE/UNKNOWN recipe evaluation", out, err)
		}
	}
	_, err = plan.Apply(ctx, sqltype.String(base, "abc"), eval.Input, eval.Output)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "23514" || diagnostic.Message != `value for domain information_schema.yes_or_no violates check constraint "yes_or_no_check"` || diagnostic.Schema != "information_schema" || diagnostic.DataType != "yes_or_no" || diagnostic.Constraint != "yes_or_no_check" || diagnostic.Table != "" || diagnostic.Column != "" || diagnostic.Detail != "" || diagnostic.Position != "" || diagnostic.Context != "" {
		t.Fatal("real FALSE recipe diagnostic", err)
	}
	if eval.domainValueActive {
		t.Fatal("VALUE frame leaked after conversion")
	}
}

func TestInfoYesOrNoBuiltinCatalog(t *testing.T) {
	source := New()
	before := source.CatalogRead()
	original := infoYesOrNoUnitCatalog(t, source, before)
	infoYesOrNoUnitEvaluate(t, before, original)
	originalRecipe := original.DomainChecks[0]
	target := New()
	if _, err := target.ReplaceCatalog(context.Background(), source, before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		view := target.CatalogRead()
		mapped := infoYesOrNoUnitCatalog(t, target, view)
		infoYesOrNoUnitEvaluate(t, view, mapped)
		// Immutable scalar codecs may be shared when their authority is
		// unchanged. The catalog-owned wrapper and recipe references must not
		// retain the source catalog's ownership (checked above).
		if mapped == original {
			t.Fatal("replacement retained source catalog wrapper")
		}
		if _, err := target.ReplaceCatalog(context.Background(), target, view); err != nil {
			t.Fatal(err)
		}
	}
	if source.CatalogRead().State() != before.State() || original.DomainChecks[0].OID != originalRecipe.OID {
		t.Fatal("replacement mutated immutable source recipe")
	}
	infoYesOrNoUnitCatalog(t, source, before)
	infoYesOrNoUnitEvaluate(t, before, original)
}

func TestInfoYesOrNoBuiltinBaseRebind(t *testing.T) {
	source, fresh := New(), New()
	originalView := source.CatalogRead()
	original := infoYesOrNoUnitCatalog(t, source, originalView)
	next := cloneCatalog(originalView.State())
	baseAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.VarChar)}
	base := *next.MustType(baseAddress)
	base.SQLType, _ = fresh.CatalogRead().LookupType(sqltype.VarChar)
	next.SetDescriptor(baseAddress, &base)
	changed, err := reownDomainTypes(next, nil, false)
	if err != nil || !changed {
		t.Fatal("actual base-generation replacement did not rebind", changed, err)
	}
	if err := source.reownCatalogExpressions(next, nil, true); err != nil {
		t.Fatal(err)
	}
	view := source.catalog.read(next)
	mapped := infoYesOrNoUnitCatalog(t, source, view)
	if mapped == original || mapped.SQLType == original.SQLType || mapped.SQLType.DomainBase().Type != base.SQLType || mapped.SQLType.DomainBase().Type == original.SQLType.DomainBase().Type {
		t.Fatal("changed base did not rebuild logical domain authority")
	}
	infoYesOrNoUnitEvaluate(t, view, mapped)
	// The cloned generation must not alter the published source or its recipe.
	if source.CatalogRead().State() != originalView.State() {
		t.Fatal("private rebind published a catalog")
	}
	infoYesOrNoUnitCatalog(t, source, originalView)
	infoYesOrNoUnitEvaluate(t, originalView, original)
}

func TestInfoYesOrNoLogicalDescriptions(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	table := domainUnitTable(t, e, op, "CREATE TABLE public.yes_or_no_description(v information_schema.yes_or_no)")
	view := e.CatalogRead()
	d, ok := view.LookupType(13256)
	if !ok || table.Columns[0].Usage != d.Usage() {
		t.Fatal("declaration lost logical domain", table.Columns)
	}
	check := func(query string) {
		t.Helper()
		bound := domainUnitBindSelect(t, e, op, query)
		schema := bound.Root.Schema()
		if len(schema) != 1 || schema[0].Usage != d.Usage() || schema[0].Usage.BaseUsage().Type.ID() != sqltype.VarChar || schema[0].Usage.BaseUsage().Typmod != 7 || schema[0].Usage.Collation.ID != 950 {
			t.Fatal("logical result description lost domain/base authority", schema)
		}
	}
	check("SELECT v FROM public.yes_or_no_description WHERE FALSE")
	// Install a private storage fixture, not a public SQL/API witness. Result
	// analysis must retain the domain even when the only physical datum is NULL.
	e.mu.Lock()
	e.storage[table.Address()] = &relationStorage{generation: 1, versions: []rowVersion{{row: 1, version: 1, born: 1, values: []sqltype.Value{sqltype.Null(d)}}}}
	e.commitSeq = 1
	e.mu.Unlock()
	check("SELECT v FROM public.yes_or_no_description")
	check("SELECT NULL::information_schema.yes_or_no AS v")
}
