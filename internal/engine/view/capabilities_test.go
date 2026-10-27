package view

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

func capabilityRelation(oid ids.OID, kind catalog.RelationKind, attrs ...int32) *catalog.Relation {
	return &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: oid}, ObjectRevision: 1, ObjectName: "capability", RelationKind: kind, Attributes: attrs}
}

func capabilityProjection(read catalog.Read, relation, source *catalog.Relation, expressions ...expr.Expr) {
	relation.View = stored.NewView(stored.ViewParts{
		Syntax:  &parser.Select{From: &parser.TableReference{}},
		Query:   &plan.Query{Root: &plan.ProjectNode{Expressions: expressions, Child: &plan.SourceNode{Occurrence: 0}}},
		Sources: []stored.ViewLeaf{{Ref: read.Domain().Ref(read.Epoch(), source.Address()), Attributes: append([]int32(nil), source.Attributes...)}},
	})
}

func capabilityBuiltin(ctx context.Context, read catalog.Read, relation *catalog.Relation, subset []int32) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return pgcatalog.BuiltinViewEvents(relation.Address().ObjectID, subset), nil
}

func TestViewCapabilitiesColumnSubset(t *testing.T) {
	state := catalog.NewState(1)
	owner := &catalog.Owner{Domain: new(ids.Domain)}
	read := catalog.NewRead(owner, state)
	base := capabilityRelation(90001, catalog.TableRelation, 2, 5)
	inner := capabilityRelation(90002, catalog.ViewRelation, 1, 2)
	outer := capabilityRelation(90003, catalog.ViewRelation, 1)
	for _, relation := range []*catalog.Relation{base, inner, outer} {
		state.SetDescriptor(relation.Address(), relation)
	}
	direct := plan.ColumnExpr{Origin: read.Domain().Ref(read.Epoch(), base.Address()), Slot: 1}
	capabilityProjection(read, inner, base, direct, plan.LiteralExpr{})
	// Live dense slots changed, but the frozen output still maps stable attr5.
	base.Attributes = []int32{5}
	for _, tc := range []struct {
		subset []int32
		want   uint32
	}{{nil, 28}, {[]int32{}, 16}, {[]int32{1}, 28}, {[]int32{2}, 16}} {
		got, err := Capabilities(context.Background(), read, inner, tc.subset, capabilityBuiltin)
		if err != nil || got != tc.want {
			t.Fatalf("subset %v: %d, %v; want %d", tc.subset, got, err, tc.want)
		}
	}
	// The same inner relation must be examined with each occurrence's subset.
	for _, tc := range []struct {
		slot int
		want uint32
	}{{0, 28}, {1, 16}} {
		capabilityProjection(read, outer, inner, plan.ColumnExpr{Origin: read.Domain().Ref(read.Epoch(), inner.Address()), Slot: tc.slot})
		got, err := Capabilities(context.Background(), read, outer, nil, capabilityBuiltin)
		if err != nil || got != tc.want {
			t.Fatalf("nested slot%d: %d, %v", tc.slot, got, err)
		}
	}
	// A frozen attribute that disappeared is an invariant failure, not NO flags.
	base.Attributes = []int32{2}
	if got, err := Capabilities(context.Background(), read, inner, nil, capabilityBuiltin); got != 0 || !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("stale attribute: %d, %v", got, err)
	}
	base.Attributes = []int32{5}
	stale := catalog.NewRead(owner, catalog.NewState(2))
	stale.State().SetDescriptor(inner.Address(), inner)
	stale.State().SetDescriptor(base.Address(), base)
	if got, err := Capabilities(context.Background(), stale, inner, nil, capabilityBuiltin); got != 0 || !errors.Is(err, ids.ErrCatalogReplaced) {
		t.Fatalf("stale lifetime: %d, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got, err := Capabilities(ctx, read, inner, nil, capabilityBuiltin); got != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled: %d, %v", got, err)
	}
	// Cyclic ancestry contributes no automatic event bits.
	capabilityProjection(read, inner, outer, plan.ColumnExpr{Origin: read.Domain().Ref(read.Epoch(), outer.Address()), Slot: 0}, plan.LiteralExpr{})
	capabilityProjection(read, outer, inner, plan.ColumnExpr{Origin: read.Domain().Ref(read.Epoch(), inner.Address()), Slot: 0})
	if got, err := Capabilities(context.Background(), read, outer, nil, capabilityBuiltin); err != nil || got != 0 {
		t.Fatalf("cycle: %d, %v", got, err)
	}
	// Builtin subset semantics remain structural, including rules and domain casts.
	for _, tc := range []struct {
		oid    ids.OID
		subset []int32
		want   uint32
	}{{12008, nil, 28}, {12008, []int32{}, 16}, {12008, []int32{3}, 16}, {12008, []int32{1}, 28}, {16202, nil, 4}, {16202, []int32{}, 4}, {16208, nil, 16}} {
		relation := capabilityRelation(tc.oid, catalog.CatalogRelation, 1, 2, 3)
		state.SetDescriptor(relation.Address(), relation)
		got, err := Capabilities(context.Background(), read, relation, tc.subset, capabilityBuiltin)
		if err != nil || got != tc.want {
			t.Fatalf("builtin%d subset%v: %d, %v", tc.oid, tc.subset, got, err)
		}
	}
}

func TestViewCapabilitiesRuleEvents(t *testing.T) {
	state := catalog.NewState(1)
	read := catalog.NewRead(&catalog.Owner{Domain: new(ids.Domain)}, state)
	relation := capabilityRelation(90100, catalog.ViewRelation)
	relation.View = stored.NewView(stored.ViewParts{Syntax: &parser.Select{}, Query: &plan.Query{Root: &plan.SingletonNode{}}})
	state.SetDescriptor(relation.Address(), relation)
	for _, tc := range []struct {
		event                string
		instead, conditional bool
		want                 uint32
	}{{"INSERT", true, false, 8}, {"UPDATE", true, false, 4}, {"DELETE", true, false, 16}, {"UPDATE", false, false, 0}, {"UPDATE", true, true, 0}, {"SELECT", true, false, 0}} {
		parts := stored.RuleParts{Event: tc.event, Mode: catalog.RuleOrigin, Instead: tc.instead}
		if tc.conditional {
			parts.Condition = plan.LiteralExpr{}
		}
		relation.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(parts)})
		for _, subset := range [][]int32{nil, {}} {
			got, err := Capabilities(context.Background(), read, relation, subset, capabilityBuiltin)
			if err != nil || got != tc.want {
				t.Fatalf("%+v subset%v: %d, %v", tc, subset, got, err)
			}
		}
	}
	relation.Rules = stored.RulesPayload([]stored.Rule{stored.NewRule(stored.RuleParts{Event: "UPDATE", Mode: catalog.RuleOrigin, Instead: true}), stored.NewRule(stored.RuleParts{Event: "DELETE", Mode: catalog.RuleOrigin, Instead: true})})
	if got, err := Capabilities(context.Background(), read, relation, nil, capabilityBuiltin); err != nil || got != 20 {
		t.Fatalf("combined rules: %d, %v", got, err)
	}
}
