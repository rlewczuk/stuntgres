package stored

import (
	"context"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// This fixture exercises detached presentation authority, not SQL binding or
// execution. Public Consumers/Persistence/TemporaryRemap cover complete queries.
func aggregateRecipeFixture(t *testing.T) (catalog.Read, *View, parser.AggregateExpr, analyze.ViewLiteralPath, *catalog.Procedure) {
	t.Helper()
	read, relation, integer, _ := routineCaptureFixture(t)
	principals := new(authorization.Domain)
	roles := authorization.Seed(principals)
	bootstrap, ok := roles.Read(principals.Principal(authorization.BootstrapPrincipalID)).Lookup("postgres")
	if !ok {
		t.Fatal("missing real bootstrap principal")
	}
	read.Owner().Principals = principals
	database := &catalog.Database{ObjectAddress: catalog.CurrentDatabaseAddress, ObjectRevision: 1, ObjectName: "capture", Owner: bootstrap.Ref}
	read.State().SetDescriptor(database.Address(), database)
	declaration := routine.BuiltinRoutine(177)
	if declaration == nil {
		t.Fatal("missing real int4pl declaration")
	}
	support := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: declaration.Identity.ID}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: declaration.Identity.Namespace}, ObjectRevision: 1, ObjectName: declaration.Identity.Name, Signature: declaration.Identity.Signature, RoutineKind: declaration.Kind, Result: declaration.Result, Strict: declaration.Strict, Volatility: declaration.Volatility, Builtin: declaration, Owner: bootstrap.Ref}
	read.State().SetDescriptor(support.Address(), support)
	aggregate := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 903}, ObjectNamespace: relation.Namespace(), ObjectRevision: 1, ObjectName: "aggregate_recipe", Signature: "23", RoutineKind: catalog.AggregateRoutine, Result: 23, Volatility: expr.Immutable, Owner: bootstrap.Ref, Aggregate: &catalog.OwnedAggregate{Definition: catalog.AggregateDefinition{Function: 903, Kind: "n", Functions: [8]ids.OID{177}, TransitionType: 23, HasInitial: true, Initial: "0", FinalModify: "r", MovingFinalModify: "r"}}}
	read.State().SetDescriptor(aggregate.Address(), aggregate)
	value, err := sqltype.Integer(integer, 1)
	if err != nil {
		t.Fatal(err)
	}
	raw := parser.AggregateExpr{Name: aggregate.Name(), Schema: &parser.Identifier{Text: "capture"}, StoredAggregate: true, Args: []parser.Expr{parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}}}
	path := analyze.ViewLiteralPath{Query: 1, Clause: analyze.ViewTargetLiteral}
	literalPath := path
	literalPath.Node = 1
	ref := read.Owner().Domain.Ref(read.Epoch(), aggregate.Address())
	view := NewView(ViewParts{ExpressionSyntax: raw, Frames: map[uint32]*plan.ProjectNode{1: {}}, Dependencies: []catalog.ExpressionDependency{{Ref: ref, Revision: aggregate.Revision()}}, RoutineRefs: map[analyze.ViewLiteralPath]ids.ObjectRef{path: ref}, Literals: map[analyze.ViewLiteralPath]plan.LiteralExpr{literalPath: {Info: expr.Info{Usage: integer.Usage()}, Value: value}}})
	return read, view, raw, path, aggregate
}

func TestCreateAggregateFrozenRecipeAuthority(t *testing.T) {
	for _, kind := range []string{"valid", "missing-marker", "missing-reference", "foreign-reference", "stale-epoch", "subobject-reference", "missing-dependency", "zero-revision", "wrong-kind", "wrong-payload", "wrong-shape", "missing-literal", "extra-reference"} {
		t.Run(kind, func(t *testing.T) {
			read, view, raw, path, aggregate := aggregateRecipeFixture(t)
			if err := view.ValidateLiterals(read); err != nil {
				t.Fatal("valid detached control refused", err)
			}
			switch kind {
			case "missing-marker":
				raw.StoredAggregate = false
			case "missing-reference":
				view.routineRefs = nil
			case "foreign-reference":
				view.routineRefs[path] = new(ids.Domain).Ref(read.Epoch(), aggregate.Address())
			case "stale-epoch":
				view.routineRefs[path] = read.Owner().Domain.Ref(read.Epoch()+1, aggregate.Address())
			case "subobject-reference":
				address := aggregate.Address()
				address.SubID = 1
				view.routineRefs[path] = read.Owner().Domain.Ref(read.Epoch(), address)
			case "missing-dependency":
				view.dependencies = nil
			case "zero-revision":
				view.dependencies[0].Revision = 0
			case "wrong-kind":
				changed := *aggregate
				changed.RoutineKind = catalog.FunctionRoutine
				read.State().SetDescriptor(changed.Address(), &changed)
			case "wrong-payload":
				changed := *aggregate
				payload := *aggregate.Aggregate
				payload.Definition.Functions[0] = 0
				changed.Aggregate = &payload
				read.State().SetDescriptor(changed.Address(), &changed)
			case "wrong-shape":
				raw.Args, raw.Star = nil, true
			case "missing-literal":
				view.literals = nil
			case "extra-reference":
				extra := path
				extra.Node = 99
				view.routineRefs[extra] = view.routineRefs[path]
			}
			view.expressionSyntax = raw
			err := view.ValidateLiterals(read)
			if (err == nil) != (kind == "valid") {
				t.Fatalf("%s detached validation=%v", kind, err)
			}
			if kind == "extra-reference" {
				return // Whole-tree counting, not one occurrence, rejects this.
			}
			renderer := NewQueryRenderer(view)
			ordinal := 0
			_, err = renderer.ExpressionSQLAt(context.Background(), read, raw, path, &ordinal, DeparseParent{})
			if (err == nil) != (kind == "valid") {
				t.Fatalf("%s detached rendering=%v", kind, err)
			}
		})
	}
}

func TestCreateAggregateTransitionFrameRemap(t *testing.T) {
	read, _, integer, boolean := routineCaptureFixture(t)
	var current *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == integer.ID() {
			current = typ
			break
		}
	}
	if current == nil || current == integer {
		t.Fatal("missing independent destination type generation")
	}
	descriptor := &catalog.Type{SQLType: current}
	read.State().SetDescriptor(descriptor.Address(), descriptor)
	parameter := plan.ParameterExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{ParameterDependent: true}}, Number: 1}
	if _, err := RemapExpression(parameter, nil, nil); err == nil {
		t.Fatal("ordinary persisted external parameter was admitted")
	}
	for _, kind := range []string{"valid", "references-only", "source-type", "source-typmod", "destination-type", "destination-typmod", "formal-count", "out-of-frame", "nonpositive", "untyped", "untyped-source-vector", "untyped-destination-vector", "references-only-destination"} {
		t.Run(kind, func(t *testing.T) {
			node := parameter
			source := []sqltype.Usage{integer.Usage()}
			destination := []sqltype.Usage{current.Usage()}
			mapper := &ViewQueryRemapper{Catalog: read, routineFormals: 64, Type: func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == integer {
					return current
				}
				return old
			}}
			want := current
			switch kind {
			case "references-only":
				mapper.referencesOnly = true
				destination[0], want = integer.Usage(), integer
			case "source-type":
				source[0] = boolean.Usage()
			case "source-typmod":
				source[0].Typmod = 99
			case "destination-type":
				destination[0] = boolean.Usage()
			case "destination-typmod":
				destination[0].Typmod = 99
			case "formal-count":
				destination = nil
			case "out-of-frame":
				node.Number = 2
			case "nonpositive":
				node.Number = 0
			case "untyped":
				node.Usage = sqltype.Usage{}
			case "untyped-source-vector":
				node.Usage, source[0] = sqltype.Usage{}, sqltype.Usage{}
			case "untyped-destination-vector":
				destination[0] = sqltype.Usage{}
			case "references-only-destination":
				mapper.referencesOnly = true
			}
			next, err := mapper.transitionExpression(node, source, destination)
			valid := kind == "valid" || kind == "references-only"
			if (err == nil) != valid {
				t.Fatalf("%s private formal mapping=%v", kind, err)
			}
			if valid {
				mapped, ok := next.(plan.ParameterExpr)
				if !ok || mapped.Number != 1 || mapped.ResultUsage() != want.Usage() || !mapped.Properties().ParameterDependent {
					t.Fatal("private formal namespace/generation lost", next)
				}
			}
			if parameter.ResultUsage().Type != integer {
				t.Fatal("source parameter generation mutated")
			}
		})
	}
}
