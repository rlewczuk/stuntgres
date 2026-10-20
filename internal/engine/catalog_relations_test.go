package engine

import (
	"context"
	"strings"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func catalogRelationsUnitBind(t *testing.T, e *Engine, name string, args ...parser.Expr) expr.Expr {
	t.Helper()
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	binder.aggregateClause = "SELECT"
	raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: name}}}, Args: args}
	node, err := binder.bindFunctionProvider(raw)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func TestCatalogRelationsBinding(t *testing.T) {
	e := New()
	scalar := catalogRelationsUnitBind(t, e, "pg_get_userbyid", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}).(functions.CatalogUserName)
	set := catalogRelationsUnitBind(t, e, "pg_get_catalog_foreign_keys").(functions.CatalogForeignKeys)
	if scalar.Validate() != nil || set.Validate() != nil {
		t.Fatal("invalid bound signature")
	}
	for _, sample := range []struct {
		node expr.Expr
		oid  uint32
	}{{scalar, 1642}, {set, 6159}} {
		reference := expr.InfoOf(sample.node).RoutineReference
		if reference == nil || reference.RoutineOID() != sample.oid {
			t.Fatal("wrong original declaration", reference)
		}
		if plannedVolatility(sample.node) != expr.Stable || generationImmutable(sample.node) || conditionalConstantInputs(sample.node) {
			t.Fatal("catalog inquiry lost STABLE symbolic semantics")
		}
	}
	value, err := scalar.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}})
	if err != nil || !value.IsNull() || value.Type().ID() != sqltype.Name {
		t.Fatal("strict typed null", value, err)
	}
	if scalar.Arg.ResultUsage().Type.ID() != sqltype.Oid || scalar.ResultUsage().Type.ID() != sqltype.Name || !sourceReturnsSet(set) || sourceReturnsSet(scalar) {
		t.Fatal("physical signature")
	}
	for _, declaration := range builtinRoutineDeclarations {
		if declaration.Identity.ID == 1642 || declaration.Identity.ID == 6159 {
			if !declaration.Strict || declaration.Volatility != expr.Stable || declaration.Parallel != 's' || declaration.ReturnsSet != (declaration.Identity.ID == 6159) {
				t.Fatal("release declaration flags", declaration)
			}
		}
	}
}

func TestCatalogRelationsConsumers(t *testing.T) {
	e := New()
	scalar := catalogRelationsUnitBind(t, e, "pg_get_userbyid", parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}})
	set := catalogRelationsUnitBind(t, e, "pg_get_catalog_foreign_keys")
	for _, sample := range []struct {
		node     expr.Expr
		name     string
		children int
	}{{scalar, "pg_get_userbyid", 1}, {set, "pg_get_catalog_foreign_keys", 0}} {
		count := 0
		boundExpressionChildren(sample.node, func(expr.Expr) { count++ })
		if count != sample.children {
			t.Fatal("typed child traversal", sample.name, count)
		}
		mapped, err := remapBoundExpression(sample.node, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, nil)
		if err != nil || !sameGroupExpression(sample.node, mapped) {
			t.Fatal("stored remap", sample.name, err)
		}
		demand := false
		text, err := (scalarRenderer{commentSnapshot: &demand}).scalarFunctionSQL(context.Background(), e.CatalogRead(), mapped)
		if err != nil || !strings.Contains(text, "pg_catalog."+sample.name+"(") || !demand {
			t.Fatal("symbolic serialization and catalog demand", text, demand, err)
		}
		if err := validateGenerationPlan(e.CatalogRead(), nil, mapped); err != nil {
			t.Fatal("typed persisted plan validation", err)
		}
		args := expr.Expr(sample.node).(expr.Composite).Children()
		reconstructed, err := expr.Substitute(expr.Expr(sample.node).(expr.Composite), expr.InfoOf(sample.node), args)
		if err != nil || !sameGroupExpression(sample.node, reconstructed) {
			t.Fatal("source reconstruction", err)
		}
	}
	// Target-list record expansion must identify the same selected occurrence
	// while constructing both the call slots and their execution schedule.
	binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	requests := []expr.Expr{set}
	root, err := binder.lowerQuerySets(&singletonNode{}, requests, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	stage, ok := root.(*setProjectNode)
	if !ok {
		t.Fatal("catalog set was not lowered")
	}
	if err := validateQuerySetSteps(stage.steps, len(stage.calls), len(stage.scalars)); err != nil {
		t.Fatal("catalog set occurrence lost its schedule", err)
	}
	if _, ok := requests[0].(columnExpression); !ok {
		t.Fatal("catalog set occurrence was not replaced by its owned slot")
	}
	fresh := New()
	destination := catalogRelationsUnitBind(t, fresh, "pg_get_catalog_foreign_keys").(functions.CatalogForeignKeys)
	mapped, _, err := retagBoundTypes(fresh.CatalogRead(), set, func(d *sqltype.Descriptor) *sqltype.Descriptor {
		if d.ID() == 2249 {
			return destination.ResultUsage().Type
		}
		value, _ := fresh.CatalogRead().LookupType(d.ID())
		return value
	})
	if err != nil {
		t.Fatal(err)
	}
	retagged := mapped.(functions.CatalogForeignKeys)
	if retagged.Validate() != nil {
		t.Fatal("invalid retagged metadata")
	}
	originalSource, err := openFunctionSource(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, set)
	if err != nil {
		t.Fatal(err)
	}
	defer originalSource.Close()
	retaggedSource, err := openFunctionSource(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, retagged)
	if err != nil {
		t.Fatal(err)
	}
	defer retaggedSource.Close()
	if originalSource.State == retaggedSource.State {
		t.Fatal("retag reused foreign descriptor metadata")
	}
	for i := 0; ; i++ {
		value, present, err := retaggedSource.Next(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		for j := range 6 {
			if value.CompositeField(j).Type() != retagged.Usage.Type.CompositeMember(j).Usage.Type {
				t.Fatal("foreign typed metadata", i, j)
			}
		}
	}
}

func TestCatalogRelationsCapture(t *testing.T) {
	e := New()
	// Publish real immutable authorization roots; no SQL, enumeration cache or
	// mocked lookup is involved. The statement keeps the original root frontier.
	e.mu.Lock()
	ref := e.roles.domain.Principal(50000)
	first := cloneRoleFixture(e.roles.published)
	putFixtureRole(first, authorization.RoleDescriptor{Ref: ref, Name: "captured é\"role", ConnectionLimit: -1})
	e.roles.published = first
	e.mu.Unlock()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer scope.Finish(context.Canceled)
	e.mu.Lock()
	err = e.captureCommentSnapshotLocked(scope)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	original := scope.commentRoles
	e.mu.Lock()
	next := cloneRoleFixture(e.roles.published)
	retireFixtureRole(next, ref)
	replacement := e.roles.domain.Principal(50001)
	putFixtureRole(next, authorization.RoleDescriptor{Ref: replacement, Name: "captured é\"role", ConnectionLimit: -1})
	e.roles.published = next
	e.mu.Unlock()
	oidType, _ := e.CatalogRead().LookupType(sqltype.Oid)
	nameType, _ := e.CatalogRead().LookupType(sqltype.Name)
	lookup := func(scope *StatementScope, oid uint32, want string) {
		t.Helper()
		input, err := sqltype.OIDValue(oidType, oid)
		if err != nil {
			t.Fatal(err)
		}
		node := functions.CatalogUserName{Info: expr.Info{Usage: nameType.Usage()}, Arg: literalExpression{Info: expr.Info{Usage: oidType.Usage()}, value: input}}
		value, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background(), statementScope: scope}})
		if err != nil {
			t.Fatal(err)
		}
		text, err := value.Text()
		if err != nil || text != want {
			t.Fatal("captured authority", text, want, err)
		}
	}
	lookup(scope, 50000, "captured é\"role")
	lookup(scope, 50001, "unknown (OID=50001)")
	e.mu.Lock()
	err = e.captureCommentSnapshotLocked(scope)
	e.mu.Unlock()
	if err != nil || scope.commentRoles != original {
		t.Fatal("statement snapshot recaptured", err)
	}
	newer, newDone := roleOperation(t, e, "postgres")
	defer newDone()
	current, err := e.StartStatement(newer)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Finish(context.Canceled)
	e.mu.Lock()
	err = e.captureCommentSnapshotLocked(current)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	lookup(current, 50000, "unknown (OID=50000)")
	lookup(current, 50001, "captured é\"role")
	lookup(current, 0, "unknown (OID=0)")
	foreign := New()
	saved := current.commentRoles
	current.commentRoles = foreign.roles.published.Root.Read(authorization.PrincipalRef{})
	input, _ := sqltype.OIDValue(oidType, 50001)
	node := functions.CatalogUserName{Info: expr.Info{Usage: nameType.Usage()}, Arg: literalExpression{Info: expr.Info{Usage: oidType.Usage()}, value: input}}
	if _, err := node.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background(), statementScope: current}}); err == nil {
		t.Fatal("foreign authorization snapshot trusted")
	}
	current.commentRoles = saved
}
