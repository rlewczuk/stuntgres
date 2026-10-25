package analyze

import (
	"context"
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The real builtin provider binds against its immutable catalog. Namespace
// selection supplies the sole qualified pg_catalog path, not a provider result.
type aggregateCaptureHost struct{ tableOIDCatalogHost }

func (h aggregateCaptureHost) BindingAuthority(base Authority) (Authority, error) { return base, nil }
func (h aggregateCaptureHost) LookupNamespaces(Authority, parser.ObjectName, LookupKind) (NamespaceSelection, error) {
	return NamespaceSelection{Qualified: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}}, nil
}

func TestRoutineAggregateDependencyPurpose(t *testing.T) {
	state := catalog.NewState(1)
	for _, typ := range sqltype.Builtins() {
		d := &catalog.Type{SQLType: typ, Array: typ.AssociatedArray()}
		state.SetDescriptor(d.Address(), d)
	}
	for _, procedure := range routine.BuiltinProcedures(authorization.PrincipalRef{}) {
		p := procedure
		state.SetDescriptor(p.Address(), &p)
	}
	read := catalog.NewRead(&catalog.Owner{Domain: new(ids.Domain)}, state)
	integer, _ := read.LookupType(sqltype.Int4)
	support, ok := read.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 177})
	if !ok {
		t.Fatal("missing int4pl")
	}
	var final *catalog.Procedure
	for _, descriptor := range state.Descriptors() {
		if p, ok := descriptor.(*catalog.Procedure); ok && p.Name() == "int4um" && p.Signature == "23" {
			final = p
			break
		}
	}
	if final == nil {
		t.Fatal("missing unary builtin final provider")
	}
	args := []expr.Expr{plan.ParameterExpr{Info: expr.Info{Usage: integer.Usage()}, Number: 1}, plan.ParameterExpr{Info: expr.Info{Usage: integer.Usage()}, Number: 2}}
	for _, directFirst := range []bool{false, true} {
		b := &Binder{Ctx: context.Background(), Host: aggregateCaptureHost{tableOIDCatalogHost{read: read}}}
		ref := read.Owner().Domain.Ref(read.Epoch(), support.Address())
		if directFirst {
			b.dependency(ref, support.Revision())
		}
		for range 2 {
			if _, err := b.bindAggregateSupport(support, args, parser.Position{}); err != nil {
				t.Fatal("real synthetic provider", err)
			}
		}
		if _, err := b.bindAggregateSupport(final, args[:1], parser.Position{}); err != nil {
			t.Fatal("real synthetic final provider", err)
		}
		finalRef := read.Owner().Domain.Ref(read.Epoch(), final.Address())
		finalCaptured := false
		for _, dependency := range b.Dependencies {
			if dependency.Ref == finalRef {
				finalCaptured = true
				if dependency.Purpose != catalog.AggregateSupportDependency {
					t.Fatal("synthetic final leaked definition lane")
				}
			}
			if !directFirst && dependency.Purpose != catalog.AggregateSupportDependency {
				t.Fatal("synthetic recapture leaked definition lane", dependency)
			}
		}
		if !finalCaptured {
			t.Fatal("synthetic final manifest missing")
		}
		if b.dependencyPurpose != catalog.DefinitionDependency {
			t.Fatal("purpose leaked after provider success")
		}
		if _, err := b.BindSelectedOperatorRoutine(support, args, parser.Position{}); err != nil {
			t.Fatal("written builtin", err)
		}
		counts := map[catalog.DependencyPurpose]int{}
		for _, dependency := range b.Dependencies {
			if dependency.Ref == ref {
				counts[dependency.Purpose]++
			}
		}
		if counts[catalog.DefinitionDependency] != 1 || counts[catalog.AggregateSupportDependency] != 1 {
			t.Fatal("same-address lanes collapsed or duplicated", counts)
		}
		bad := *support
		bad.Signature = "25"
		if _, err := b.bindAggregateSupport(&bad, args, parser.Position{}); err == nil {
			t.Fatal("invalid selected provider accepted")
		}
		if b.dependencyPurpose != catalog.DefinitionDependency {
			t.Fatal("purpose leaked after provider error")
		}
		other := ref
		other.Address.ObjectID = 99999
		b.dependency(other, 1)
		if b.Dependencies[len(b.Dependencies)-1].Purpose != catalog.DefinitionDependency {
			t.Fatal("later written capture swallowed by support scope")
		}
		previous := catalog.AggregateSupportDependency
		b.dependencyPurpose = previous
		if _, err := b.bindAggregateSupport(&bad, args, parser.Position{}); err == nil {
			t.Fatal("invalid nested provider accepted")
		}
		if b.dependencyPurpose != previous {
			t.Fatal("enclosing purpose not restored")
		}
	}
}

func TestSQLRoutineGroupProperties(t *testing.T) {
	fixture, _ := tableOIDBinder(t)
	read := fixture.BindingCatalog()
	integer, _ := read.LookupType(sqltype.Int4)
	literal := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}}
	parameter := plan.ParameterExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{ParameterDependent: true}}, Number: 1}
	grouped := plan.AggregateExpr{Info: expr.Info{Usage: integer.Usage(), Property: expr.Properties{StageDependent: true}}, Frame: 1}
	for _, volatility := range []expr.Volatility{expr.Immutable, expr.Stable, expr.Volatile} {
		for _, child := range []expr.Expr{literal, parameter, grouped} {
			d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 90001}, ObjectRevision: 1, Result: ids.OID(sqltype.Int4), Volatility: volatility, SQL: &proc.Definition{Inputs: []sqltype.Usage{integer.Usage()}, Result: integer.Usage()}}
			properties := expr.MergeProperties(expr.Properties{Volatility: volatility}, child.Properties())
			call := plan.SQLRoutineExpr{Info: expr.Info{Usage: integer.Usage(), RoutineReference: d, Property: properties}, Procedure: d, Ref: read.Owner().Domain.Ref(read.Epoch(), d.Address()), Args: []expr.Expr{child}, WrittenCount: 1, InputMap: []plan.RoutineInput{{Start: 0, Count: 1}}, ChildSlots: []int{0}}
			b := &Binder{Ctx: context.Background(), Query: QueryScopeState{Frame: 1}}
			rewritten, err := b.RewriteGroup(call, nil)
			if err != nil {
				t.Fatal(err)
			}
			next := rewritten.(plan.SQLRoutineExpr)
			if next.Properties() != properties {
				t.Fatal("group context tainted scalar properties", next.Properties(), properties)
			}
			if err := next.ValidateStoredProvider(); err != nil {
				t.Fatal("valid grouped call rejected", err)
			}
			if call.Args[0] != child || call.Properties() != properties {
				t.Fatal("group rewrite mutated source")
			}
			outer := call
			outerDeclaration := *d
			outerDeclaration.Volatility = expr.Immutable
			outer.Procedure, outer.Info.RoutineReference = &outerDeclaration, &outerDeclaration
			outer.Args = []expr.Expr{call}
			outer.Property = properties
			nested, err := b.RewriteGroup(outer, nil)
			if err != nil {
				t.Fatal("nested group rewrite", err)
			}
			nestedCall := nested.(plan.SQLRoutineExpr)
			if nestedCall.Properties() != properties {
				t.Fatal("nested call lost merged properties")
			}
			if err := nestedCall.ValidateStoredProvider(); err != nil {
				t.Fatal("nested valid call rejected", err)
			}
			malformed := next
			malformed.Property.StageDependent = !properties.StageDependent
			if err := malformed.ValidateStoredProvider(); err == nil {
				t.Fatal("strict property validation weakened")
			}
		}
	}
}
