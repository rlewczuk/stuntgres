package enginetests_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func infoCardinalNumberUnitCatalog(t *testing.T, e *engine.Engine, view catalog.Read) *catalog.Type {
	t.Helper()
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 13243}
	d, ok := view.Type(address)
	if !ok || d.Name() != "cardinal_number" || d.Namespace() != (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 13229}) || d.ObjectRevision == 0 || !d.SQLType.IsBuiltin() || !d.SQLType.Concrete() || !d.SQLType.HasDomainChecks() {
		t.Fatal("selected domain wrapper", d)
	}
	base, ok := view.LookupType(sqltype.Int4)
	if !ok || d.SQLType.DomainBase() != (sqltype.Usage{Type: base, Typmod: -1}) {
		t.Fatal("selected immediate base generation", d.SQLType.DomainBase())
	}
	namespace, ok := view.Descriptor(d.Namespace())
	if !ok || namespace.Name() != "information_schema" {
		t.Fatal("selected namespace", namespace)
	}
	array, ok := view.LookupType(13242)
	if !ok || array.Element() != d.SQLType || array.Concrete() || d.Array != 13242 {
		t.Fatal("selected companion backlink", array)
	}
	if len(d.DomainChecks) != 1 {
		t.Fatal("own recipe count", len(d.DomainChecks))
	}
	check := d.DomainChecks[0]
	if check.OID == 0 || check.Name != "cardinal_number_domain_check" || check.NotValid || check.Ordinal != 0 || check.Expression == nil || len(check.Dependencies) == 0 || !check.Expression.Properties().DomainDependent || check.Expression.Properties().RowDependent {
		t.Fatal("real validated VALUE recipe", check)
	}
	for _, dependency := range check.Dependencies {
		provider, ok := view.Descriptor(dependency.Ref.Address)
		if !ok || dependency.Ref.Domain() != e.Catalog.Domain || dependency.Ref.Epoch != view.Epoch() || dependency.Revision != provider.Revision() {
			t.Fatal("captured recipe dependency", dependency)
		}
	}
	valueLeaves := 0
	plan.WalkExpression(check.Expression, func(node expr.Expr) {
		if _, ok := node.(plan.DomainValueExpr); ok {
			valueLeaves++
			if node.ResultUsage() != d.SQLType.DomainBase() {
				t.Fatal("VALUE lost immediate base/typmod authority", node.ResultUsage())
			}
		}
	})
	if valueLeaves == 0 {
		t.Fatal("recipe does not consume real VALUE")
	}
	if err := e.Catalog.Providers[ids.TypeClassID].Validate(view, d); err != nil {
		t.Fatal(err)
	}
	if err := catalog.ValidateDefinitionOIDs(view); err != nil {
		t.Fatal(err)
	}
	return d
}

func infoCardinalNumberUnitInteger(t *testing.T, base *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	v, err := sqltype.Integer(base, n)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func infoCardinalNumberUnitEvaluate(t *testing.T, view catalog.Read, d *catalog.Type) {
	t.Helper()
	base := d.SQLType.DomainBase().Type
	ctx := context.Background()
	eval := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx, Catalog: view}}
	eval.Input.DomainChecks = &eval
	plan, err := sqltype.PlanCoercion(base.Usage(), d.SQLType.Usage(), sqltype.Assignment)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []sqltype.Value{infoCardinalNumberUnitInteger(t, base, 0), infoCardinalNumberUnitInteger(t, base, 7), infoCardinalNumberUnitInteger(t, base, 2147483647), sqltype.Null(base)} {
		out, err := plan.Apply(ctx, value, eval.Input, eval.Output)
		if err != nil || out.Type() != d.SQLType || out.IsNull() != value.IsNull() {
			t.Fatal("real TRUE/UNKNOWN recipe evaluation", out, err)
		}
	}
	_, err = plan.Apply(ctx, infoCardinalNumberUnitInteger(t, base, -1), eval.Input, eval.Output)
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23514", Message: `value for domain information_schema.cardinal_number violates check constraint "cardinal_number_domain_check"`, Schema: "information_schema", DataType: "cardinal_number", Constraint: "cardinal_number_domain_check"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatal("real FALSE recipe diagnostic", err)
	}
	if eval.DomainValueActive {
		t.Fatal("VALUE frame leaked after conversion")
	}
}

func TestInfoCardinalNumberBuiltinCatalog(t *testing.T) {
	source := engine.New()
	before := source.CatalogRead()
	original := infoCardinalNumberUnitCatalog(t, source, before)
	infoCardinalNumberUnitEvaluate(t, before, original)
	originalRecipe := original.DomainChecks[0]
	target := engine.New()
	if _, err := target.ReplaceCatalog(context.Background(), source, before); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		view := target.CatalogRead()
		mapped := infoCardinalNumberUnitCatalog(t, target, view)
		infoCardinalNumberUnitEvaluate(t, view, mapped)
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
	infoCardinalNumberUnitCatalog(t, source, before)
	infoCardinalNumberUnitEvaluate(t, before, original)
}

func TestInfoCardinalNumberBuiltinBaseRebind(t *testing.T) {
	source, fresh := engine.New(), engine.New()
	originalView := source.CatalogRead()
	original := infoCardinalNumberUnitCatalog(t, source, originalView)
	next := catalog.CloneState(originalView.State())
	baseAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Int4)}
	base := *next.MustType(baseAddress)
	base.SQLType, _ = fresh.CatalogRead().LookupType(sqltype.Int4)
	next.SetDescriptor(baseAddress, &base)
	changed, err := typedef.ReownDomainTypes(next, nil, false, routine.RangeTypeRebinder(next, nil))
	if err != nil || !changed {
		t.Fatal("actual base-generation replacement did not rebind", changed, err)
	}
	if err := stored.ReownExpressions(source.Catalog.Read(next), next, nil, true, nil); err != nil {
		t.Fatal(err)
	}
	view := source.Catalog.Read(next)
	mapped := infoCardinalNumberUnitCatalog(t, source, view)
	if mapped == original || mapped.SQLType == original.SQLType || mapped.SQLType.DomainBase().Type != base.SQLType || mapped.SQLType.DomainBase().Type == original.SQLType.DomainBase().Type {
		t.Fatal("changed base did not rebuild logical domain authority")
	}
	infoCardinalNumberUnitEvaluate(t, view, mapped)
	// The cloned generation must not alter the published source or its recipe.
	if source.CatalogRead().State() != originalView.State() {
		t.Fatal("private rebind published a catalog")
	}
	infoCardinalNumberUnitCatalog(t, source, originalView)
	infoCardinalNumberUnitEvaluate(t, originalView, original)
}

func TestInfoCardinalNumberLogicalDescriptions(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	table := domainUnitTable(t, e, op, "CREATE TABLE public.cardinal_number_description(v information_schema.cardinal_number)")
	view := e.CatalogRead()
	d, ok := view.LookupType(13243)
	if !ok || table.Columns[0].Usage != d.Usage() {
		t.Fatal("declaration lost logical domain", table.Columns)
	}
	check := func(query string) {
		t.Helper()
		bound := domainUnitBindSelect(t, e, op, query)
		schema := bound.Root.Schema()
		if len(schema) != 1 || schema[0].Usage != d.Usage() || schema[0].Usage.BaseUsage().Type.ID() != sqltype.Int4 || schema[0].Usage.BaseUsage().Typmod != -1 || schema[0].Usage.Collation != (sqltype.CollationRef{}) {
			t.Fatal("logical result description lost domain/base authority", schema)
		}
	}
	check("SELECT v FROM public.cardinal_number_description WHERE FALSE")
	// Install a private storage fixture, not a public SQL/API witness. Result
	// analysis must retain the domain even when the only physical datum is NULL.
	e.Mu.Lock()
	e.Storage.InstallRelationLocked(table.Address(), &storage.Relation{Generation: 1, Versions: []storage.RowVersion{{Row: 1, Version: 1, Born: 1, Values: []sqltype.Value{sqltype.Null(d)}}}})
	e.Visibility.SetCommitSeqLocked(1)
	e.Mu.Unlock()
	check("SELECT v FROM public.cardinal_number_description")
	check("SELECT NULL::information_schema.cardinal_number AS v")
}

func domainUnitTable(t *testing.T, e *engine.Engine, op *session.Operation, q string) *catalog.Relation {
	t.Helper()
	raw := enginetest.Statement(t, q).(*parser.CreateTable)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	stage := e.BeginCatalogStage(auth.Catalog)
	if err == nil {
		err = e.SchemaTableLocked(op, auth, stage, raw, ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, sqltype.InputEnvironment{}, nil)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	domainUnitPublish(t, e, stage)
	view := e.CatalogRead()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, Name: raw.Name.Parts[len(raw.Name.Parts)-1].Text})
	if len(addresses) != 1 {
		t.Fatal(addresses)
	}
	return view.MustRelation(addresses[0])
}

func domainUnitBindSelect(t *testing.T, e *engine.Engine, op *session.Operation, q string) *plan.Query {
	t.Helper()
	raw := enginetest.Statement(t, q).(*parser.Select)
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	b := engine.NewExpressionBinder(op.Context(), e, sqltype.InputEnvironment{}, op, nil)
	b.Query.Auth = &auth
	var query *plan.Query
	if err == nil {
		query, err = b.BindSelect(raw, false)
	}
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(q, err)
	}
	return query
}

func domainUnitPublish(t *testing.T, e *engine.Engine, stage *engine.CatalogStage) {
	t.Helper()
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
}
