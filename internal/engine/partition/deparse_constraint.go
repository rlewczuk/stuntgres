package partition

import (
	"context"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func deparseConstraint(ctx context.Context, r stored.Deparser, relation *catalog.Relation) (string, error) {
	type edge struct {
		parent *catalog.Relation
		bound  *catalog.PartitionMembership
	}
	var edges []edge
	for current := relation; current.Membership != nil; {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		parent, ok := r.View.Relation(current.Membership.Parent)
		if !ok || parent.Partition == nil || parent.Address() == relation.Address() {
			return "", invalidDeparse()
		}
		for _, previous := range edges {
			if previous.parent.Address() == parent.Address() {
				return "", invalidDeparse()
			}
		}
		edges = append(edges, edge{parent, current.Membership})
		current = parent
	}
	var clauses []qualification
	for i := len(edges) - 1; i >= 0; i-- {
		builder, err := newQualDeparser(ctx, r, edges[i].parent, relation)
		if err != nil {
			return "", err
		}
		local, err := builder.membership(edges[i].bound)
		if err != nil {
			return "", err
		}
		clauses = append(clauses, local...)
	}
	if len(clauses) == 0 {
		return "", nil
	}
	var out strings.Builder
	if err := qualGroup("AND", clauses).appendSQL(ctx, &out); err != nil {
		return "", err
	}
	return out.String(), nil
}

type qualDeparser struct {
	ctx     context.Context
	r       stored.Deparser
	parent  *catalog.Relation
	keys    []expr.Expr
	boolean *sqltype.Descriptor
}

func newQualDeparser(ctx context.Context, r stored.Deparser, parent, target *catalog.Relation) (qualDeparser, error) {
	q := qualDeparser{ctx: ctx, r: r, parent: parent}
	p := parent.Partition
	if p == nil || len(p.Keys) == 0 || len(parent.Columns) != len(parent.Attributes) || len(target.Columns) != len(target.Attributes) {
		return q, invalidDeparse()
	}
	var ok bool
	q.boolean, ok = r.View.LookupType(sqltype.Bool)
	if !ok {
		return q, invalidDeparse()
	}
	mapping := analyze.LikeInput{Source: parent, TargetSlots: make([]int, len(parent.Columns))}
	for i, column := range parent.Columns {
		if err := ctx.Err(); err != nil {
			return q, err
		}
		slot, ok := target.Index[column.Name]
		if !ok || slot < 0 || slot >= len(target.Columns) {
			return q, invalidDeparse()
		}
		if target.Columns[slot].Name != column.Name || target.Columns[slot].Usage.Type != column.Usage.Type {
			return q, invalidDeparse()
		}
		mapping.TargetSlots[i] = slot
	}
	q.keys = make([]expr.Expr, len(p.Keys))
	for i, key := range p.Keys {
		if err := ctx.Err(); err != nil {
			return q, err
		}
		method := ids.OID(403)
		if p.Strategy == parser.PartitionHash {
			method = 405
		}
		if _, err := deparseClass(r.View, key, method); err != nil {
			return q, err
		}
		if key.Derived != nil {
			if key.Derived.Definition == nil || key.Derived.Definition.ResultUsage().Type != key.Usage.Type {
				return q, invalidDeparse()
			}
			node, err := stored.RemapLikeExpression(&mapping, key.Derived.Definition, target, r.View)
			if err != nil {
				return q, err
			}
			q.keys[i] = node
		} else {
			parentSlot := parent.IndexForAttribute(key.Attribute)
			if parentSlot < 0 {
				return q, invalidDeparse()
			}
			if parent.Columns[parentSlot].Usage.Type != key.Usage.Type {
				return q, invalidDeparse()
			}
			slot := mapping.TargetSlots[parentSlot]
			ref := r.View.Owner().Domain.Ref(r.View.Epoch(), target.Address())
			ref.Address.SubID = target.Attributes[slot]
			q.keys[i] = plan.ColumnExpr{Info: expr.Info{Usage: target.Columns[slot].Usage, Property: expr.Properties{RowDependent: true}}, Origin: ref, Scope: 1, Frame: 1, Slot: slot}
		}
	}
	return q, nil
}

func (q qualDeparser) leaf(node expr.Expr) (qualification, error) {
	text, err := q.r.SQL(q.ctx, node, stored.DeparseParent{})
	return qualification{text: text}, err
}

func (q qualDeparser) nullTest(i int, not bool) (qualification, error) {
	return q.leaf(plan.NullTestExpr{Info: expr.Info{Usage: q.boolean.Usage()}, Child: q.keys[i], Not: not})
}

func (q qualDeparser) nullTests() ([]qualification, error) {
	terms := make([]qualification, len(q.keys))
	for i := range terms {
		var err error
		terms[i], err = q.nullTest(i, true)
		if err != nil {
			return nil, err
		}
	}
	return terms, nil
}

func (q qualDeparser) membership(bound *catalog.PartitionMembership) ([]qualification, error) {
	if err := q.ctx.Err(); err != nil {
		return nil, err
	}
	if bound.Strategy != q.parent.Partition.Strategy {
		return nil, invalidDeparse()
	}
	switch bound.Strategy {
	case parser.PartitionRange:
		return q.rangeQual(bound, false)
	case parser.PartitionList:
		return q.listQual(bound)
	case parser.PartitionHash:
		return q.hashQual(bound)
	default:
		return nil, invalidDeparse()
	}
}

func (q qualDeparser) hashQual(bound *catalog.PartitionMembership) ([]qualification, error) {
	if !ValidHashBound(bound) {
		return nil, invalidDeparse()
	}
	oid, oidOK := q.r.View.LookupType(sqltype.Oid)
	integer, integerOK := q.r.View.LookupType(sqltype.Int4)
	anyType, anyOK := q.r.View.LookupType(2276)
	identity, identityOK := catalog.BuiltinRoutineIdentityByOID(5028)
	if !oidOK || !integerOK || !anyOK || !identityOK {
		return nil, invalidDeparse()
	}
	parent, err := sqltype.OIDValue(oid, uint32(q.parent.Address().ObjectID))
	if err != nil {
		return nil, err
	}
	modulus, err := sqltype.Integer(integer, int64(bound.Modulus))
	if err != nil {
		return nil, err
	}
	remainder, err := sqltype.Integer(integer, int64(bound.Remainder))
	if err != nil {
		return nil, err
	}
	args := make([]expr.Expr, 3+len(q.keys))
	for i, value := range [...]sqltype.Value{parent, modulus, remainder} {
		args[i] = plan.LiteralExpr{Info: expr.Info{Usage: value.Type().Usage()}, Value: value}
	}
	copy(args[3:], q.keys)
	fixed := [...]sqltype.Usage{oid.Usage(), integer.Usage(), integer.Usage()}
	// Lookup-only release identity and expanded formals are presentation
	// authority; they never install or invoke the separate HASH predicate.
	name, err := q.r.VariadicRoutineName(q.ctx, identity, fixed[:], anyType.Usage(), args)
	if err != nil {
		return nil, err
	}
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i], err = q.r.SQL(q.ctx, arg, stored.DeparseParent{})
		if err != nil {
			return nil, err
		}
	}
	return []qualification{{text: name + "(" + strings.Join(parts, ", ") + ")"}}, nil
}

// comparisonOperands authenticates the saved family and its binary-compatible
// input adapter. It never evaluates a bound or rewrites its declared type.
func (q qualDeparser) comparisonOperands(i int, strategy int16) (expr.Expr, *catalog.Operator, string, error) {
	key := q.parent.Partition.Keys[i]
	op, err := BoundOperator(q.r.View, key, strategy)
	if err != nil {
		return nil, nil, "", err
	}
	class, ok := q.r.View.OperatorClass(ids.OID(key.Class.OID()))
	if !ok {
		return nil, nil, "", invalidDeparse()
	}
	if op.Left != class.InputType || op.Right != class.InputType || op.Result != ids.OID(q.boolean.ID()) {
		return nil, nil, "", invalidDeparse()
	}
	input, ok := q.r.View.LookupType(sqltype.ID(class.InputType))
	if !ok {
		return nil, nil, "", invalidDeparse()
	}
	left := q.keys[i]
	if input.Concrete() && (input != key.Usage.Type || key.Usage.Collation.ID != left.ResultUsage().Collation.ID) {
		if _, ok := catalog.ImplicitBinaryCast(q.r.View, key.Usage.Type.ID(), input.ID()); !ok {
			return nil, nil, "", invalidDeparse()
		}
		usage := key.Usage
		usage.Type, usage.Typmod = input, -1
		conversion, err := sqltype.PlanDeclaredBinaryCoercion(left.ResultUsage(), usage)
		if err != nil {
			return nil, nil, "", err
		}
		left = plan.CoercionExpr{Info: expr.Info{Usage: usage, Property: left.Properties()}, Child: left, Plan: conversion, Mode: sqltype.Explicit}
	}
	namespace, err := q.operatorNamespace(op)
	return left, op, namespace, err
}

func (q qualDeparser) operatorNamespace(op *catalog.Operator) (string, error) {
	namespace, ok := q.r.View.Namespace(op.Namespace())
	if !ok {
		return "", invalidDeparse()
	}
	if q.r.Path == nil && op.Namespace().ObjectID == ids.PGCatalogNamespaceOID {
		return "", nil
	}
	index := q.r.View.State().OperatorNames()
	if index == nil {
		return "", invalidDeparse()
	}
	for _, ns := range q.r.Path {
		if err := q.ctx.Err(); err != nil {
			return "", err
		}
		for _, address := range index.Buckets[catalog.OperatorNameKey{Namespace: ns, Name: op.Name()}] {
			visible, ok := q.r.View.Operator(address)
			if !ok {
				return "", invalidDeparse()
			}
			if visible.Left == op.Left && visible.Right == op.Right {
				if visible.Address() == op.Address() {
					return "", nil
				}
				// Passing pg_catalog here would hide qualification in the
				// ordinary renderer, so retain the full selected spelling.
				return namespace.Name(), nil
			}
		}
	}
	return namespace.Name(), nil
}

func (q qualDeparser) datum(i int, datum catalog.PartitionDatum) (expr.Expr, error) {
	key := q.parent.Partition.Keys[i]
	if datum.Kind != 0 || datum.Value.Type() != key.Usage.Type || datum.Value.IsNull() {
		return nil, invalidDeparse()
	}
	// The effective collation belongs to the Const's Usage, not to an
	// explicit COLLATE expression with its own grouping.
	return plan.LiteralExpr{Info: expr.Info{Usage: key.Usage}, Value: datum.Value}, nil
}

func (q qualDeparser) compare(i int, strategy int16, datum catalog.PartitionDatum) (qualification, error) {
	left, op, ns, err := q.comparisonOperands(i, strategy)
	if err != nil {
		return qualification{}, err
	}
	right, err := q.datum(i, datum)
	if err != nil {
		return qualification{}, err
	}
	operator := op.Name()
	if ns == "pg_catalog" {
		operator, ns = "OPERATOR("+q.r.Identifier(ns)+"."+operator+")", ""
	}
	return q.leaf(plan.ComparisonExpr{Info: expr.Info{Usage: q.boolean.Usage()}, Left: left, Right: right, Operator: operator, Namespace: ns})
}

func (q qualDeparser) requireOrdering(i int) error {
	key := q.parent.Partition.Keys[i]
	current, err := routine.BindIndexOrdering(q.r.View, key.Usage, key.Class)
	if err != nil {
		return err
	}
	if !current.SameOperation(key.Comparison) {
		return invalidDeparse()
	}
	return nil
}
