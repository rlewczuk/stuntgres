package sqltype

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/fts"
)

type FTSQueryFunction uint8

const (
	FTSNumNode FTSQueryFunction = iota
	FTSQueryTree
	FTSRewrite
	FTSRewriteSQL
)

func (o FTSQueryFunction) Name() string {
	switch o {
	case FTSNumNode:
		return "numnode"
	case FTSQueryTree:
		return "querytree"
	case FTSRewrite, FTSRewriteSQL:
		return "ts_rewrite"
	}
	return ""
}
func (o FTSQueryFunction) Arity() int {
	switch o {
	case FTSNumNode, FTSQueryTree:
		return 1
	case FTSRewrite:
		return 3
	case FTSRewriteSQL:
		return 2
	}
	return 0
}
func (o FTSQueryFunction) Formal(i int) ID {
	if o == FTSRewriteSQL && i == 1 {
		return Text
	}
	return TSQuery
}
func (o FTSQueryFunction) ResultID() ID {
	switch o {
	case FTSNumNode:
		return Int4
	case FTSQueryTree:
		return Text
	}
	return TSQuery
}

type FTSQueryPlan struct {
	operation FTSQueryFunction
	inputs    [3]*Descriptor
	result    *Descriptor
}

func BindFTSQuery(op FTSQueryFunction, result Usage, args [3]Usage) (FTSQueryPlan, error) {
	if op.Name() == "" || result.Type == nil || result.Type.id != op.ResultID() || result.Typmod != result.Type.Usage().Typmod || result.Type.id != Text && result != result.Type.Usage() {
		return FTSQueryPlan{}, fmt.Errorf("invalid query function signature")
	}
	p := FTSQueryPlan{operation: op, result: result.Type}
	for i, arg := range args {
		if i >= op.Arity() {
			if arg.Type != nil {
				return FTSQueryPlan{}, fmt.Errorf("extra query function operand")
			}
			continue
		}
		if arg.Type == nil || arg.Type.id != op.Formal(i) || arg.Typmod != arg.Type.Usage().Typmod || arg.Type.id != Text && arg != arg.Type.Usage() {
			return FTSQueryPlan{}, fmt.Errorf("invalid query function operand")
		}
		p.inputs[i] = arg.Type
	}
	source := p.inputs[0]
	for i := 1; i < op.Arity(); i++ {
		expected := source
		if op.Formal(i) == Text {
			expected = source.ftsText
		}
		if p.inputs[i] != expected {
			return FTSQueryPlan{}, fmt.Errorf("foreign query function operand authority")
		}
	}
	expected := source
	if op == FTSNumNode {
		expected = source.ftsDistance
	}
	if op == FTSQueryTree {
		expected = source.ftsText
	}
	if p.result != expected {
		return FTSQueryPlan{}, fmt.Errorf("foreign query function result authority")
	}
	return p, nil
}
func (p FTSQueryPlan) Operation() FTSQueryFunction { return p.operation }
func (p FTSQueryPlan) ValidateValues(values [3]Value) error {
	if p.result == nil || p.operation.Name() == "" {
		return fmt.Errorf("invalid query function plan")
	}
	var args [3]Usage
	for i := range p.operation.Arity() {
		v := values[i]
		if v.typ == nil || v.typ != p.inputs[i] {
			return fmt.Errorf("foreign query function value")
		}
		if !v.null && v.typ.id == TSQuery && !v.validFTS() {
			return fmt.Errorf("invalid query function payload")
		}
		args[i] = v.typ.Usage()
	}
	checked, err := BindFTSQuery(p.operation, p.result.Usage(), args)
	if err != nil || checked != p {
		return fmt.Errorf("invalid query function plan authority")
	}
	return nil
}
func (p FTSQueryPlan) Evaluate(ctx context.Context, values [3]Value) (Value, error) {
	if err := ctx.Err(); err != nil {
		return Value{}, err
	}
	if err := p.ValidateValues(values); err != nil {
		return Value{}, err
	}
	for _, v := range values[:p.operation.Arity()] {
		if v.null {
			return Null(p.result), nil
		}
	}
	result := Value{typ: p.result}
	var err error
	switch p.operation {
	case FTSNumNode:
		result.integer = int64(values[0].ftsQuery.NodeCount())
	case FTSQueryTree:
		result.text, err = values[0].ftsQuery.IndexableTree(ctx)
	case FTSRewrite:
		result.ftsQuery, err = values[0].ftsQuery.Rewrite(ctx, values[1].ftsQuery, values[2].ftsQuery)
	default:
		return Value{}, fmt.Errorf("SQL rewrite requires statement runtime")
	}
	if err != nil {
		return Value{}, err
	}
	return result, nil
}

func (v Value) EmptyTSQuery() (bool, error) {
	if v.typ == nil || v.typ.id != TSQuery || v.null || !v.validFTS() {
		return false, fmt.Errorf("invalid tsquery value")
	}
	return v.ftsQuery.Empty(), nil
}

// FTSRewriteRules is an owner-checked bridge, not a raw query-payload accessor.
// SQL NULL rule cells skip the row; an empty non-NULL substitute deletes.
type FTSRewriteRules struct {
	owner     *Descriptor
	workspace *fts.RewriteWorkspace
}

func NewFTSRewriteRules(ctx context.Context, initial Value, result *Descriptor) (*FTSRewriteRules, error) {
	if result == nil || result.id != TSQuery || initial.typ != result || initial.null || !initial.validFTS() {
		return nil, fmt.Errorf("invalid rewrite result authority")
	}
	workspace, err := fts.NewRewriteWorkspace(ctx, initial.ftsQuery)
	if err != nil {
		return nil, err
	}
	return &FTSRewriteRules{owner: result, workspace: workspace}, nil
}
func (r *FTSRewriteRules) Apply(target, substitute Value) error {
	if r == nil || r.workspace == nil || r.owner == nil || target.typ != r.owner || substitute.typ != r.owner {
		return fmt.Errorf("foreign rewrite rule authority")
	}
	if !target.null && !target.validFTS() || !substitute.null && !substitute.validFTS() {
		return fmt.Errorf("invalid rewrite rule payload")
	}
	if target.null || substitute.null {
		return nil
	}
	return r.workspace.ApplyRule(target.ftsQuery, substitute.ftsQuery)
}

// Empty exposes only checked accumulator state, never the mutable query arena.
func (r *FTSRewriteRules) Empty() (bool, error) {
	if r == nil || r.workspace == nil || r.owner == nil || r.owner.id != TSQuery {
		return false, fmt.Errorf("invalid rewrite accumulator")
	}
	return r.workspace.Empty(), nil
}
func (r *FTSRewriteRules) Finish() (Value, error) {
	if r == nil || r.workspace == nil || r.owner == nil {
		return Value{}, fmt.Errorf("invalid rewrite accumulator")
	}
	q, err := r.workspace.Finish()
	if err != nil {
		return Value{}, err
	}
	return Value{typ: r.owner, ftsQuery: q}, nil
}
