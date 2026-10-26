package enginetests_test

import (
	"context"
	"fmt"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Construct through the type-owned binary mapping: FLOAT4 signaling NaN words
// must not first pass through Go float32/float64 conversion and lose raw bits.
func preparedFoldFloatWord(t *testing.T, e *core.Engine, id sqltype.ID, word uint64) sqltype.Value {
	t.Helper()
	sourceID := sqltype.Int8
	n := int64(word)
	if id == sqltype.Float4 {
		sourceID = sqltype.Int4
		n = int64(int32(uint32(word)))
	}
	source, ok := e.CatalogRead().LookupType(sourceID)
	if !ok {
		t.Fatal("missing integer descriptor")
	}
	target, ok := e.CatalogRead().LookupType(id)
	if !ok {
		t.Fatal("missing float descriptor")
	}
	value, err := sqltype.Integer(source, n)
	if err != nil {
		t.Fatal(err)
	}
	mapping, err := sqltype.PlanBinaryCoercion(source, target)
	if err != nil {
		t.Fatal(err)
	}
	value, err = mapping.Apply(context.Background(), value)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// These fixtures contain literal/omitted inputs only. Unlike the composed
// owner's published provider payloads, a missing typed datum here is invalid.
func sameLiteralFold(a, b exec.FoldConstant) bool {
	callable := a.Callable == b.Callable
	if !callable {
		left, lok := a.Callable.(*catalog.Procedure)
		right, rok := b.Callable.(*catalog.Procedure)
		callable = lok && rok && exec.SameRoutineDeclaration(left, right)
	}
	if a.Offset != b.Offset || a.Occurrence != b.Occurrence || a.Source != b.Source || a.Origin != b.Origin || a.Frame != b.Frame || a.Provider != b.Provider || !callable || a.Operator != b.Operator || a.Result != b.Result || !sqltype.SameDatum(a.Value, b.Value) || !a.Coercion.Equal(b.Coercion) || len(a.Inputs) != len(b.Inputs) {
		return false
	}
	for i, input := range a.Inputs {
		other := b.Inputs[i]
		if input.Usage != other.Usage || input.UnknownNull != other.UnknownNull {
			return false
		}
		if input.Usage == (sqltype.Usage{}) && input.Value.Type() == nil && other.Value.Type() == nil && !input.UnknownNull {
			continue
		}
		if !sqltype.SameDatum(input.Value, other.Value) {
			return false
		}
	}
	return true
}

func TestPreparedFoldDatumIdentity(t *testing.T) {
	e := engine.New()
	for _, tc := range []struct {
		name string
		id sqltype.ID
		words []uint64
	}{
		{"float4", sqltype.Float4, []uint64{0x7fc00001, 0x7fc00002, 0x7f800001, 0xffc00001, 0, 0x80000000}},
		{"float8", sqltype.Float8, []uint64{0x7ff8000000000001, 0x7ff8000000000002, 0x7ff0000000000001, 0xfff8000000000001, 0, 0x8000000000000000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for i, word := range tc.words {
				value := preparedFoldFloatWord(t, e, tc.id, word)
				usage := value.Type().Usage()
				constant := exec.FoldConstant{Result: usage, Value: value, Inputs: []exec.FoldInput{{Usage: usage, Value: value}}}
				t.Run(fmt.Sprintf("%016x", word), func(t *testing.T) {
					// Independently chosen IEEE words, not SQL NaN equality.
					copyValue := preparedFoldFloatWord(t, e, tc.id, word)
					if !sqltype.SameDatum(value, copyValue) {
						t.Fatal("type-owned identity lost identical physical word")
					}
					copyConstant := constant
					copyConstant.Value = copyValue
					copyConstant.Inputs = []exec.FoldInput{{Usage: usage, Value: copyValue}}
					if !sameLiteralFold(constant, copyConstant) {
						t.Error("identical NaN/zero artifact is not reflexive")
					}
					for j, otherWord := range tc.words {
						if i == j {
							continue
						}
						other := preparedFoldFloatWord(t, e, tc.id, otherWord)
						if sqltype.SameDatum(value, other) {
							t.Fatal("distinct IEEE words collapsed")
						}
						changedResult := constant
						changedResult.Value = other
						changedInput := constant
						changedInput.Inputs = []exec.FoldInput{{Usage: usage, Value: other}}
						if sameLiteralFold(constant, changedResult) || sameLiteralFold(constant, changedInput) {
							t.Error("distinct result/input representation inherited artifact")
						}
					}
					conversion, err := sqltype.PlanCoercion(usage, usage, sqltype.Explicit)
					if err != nil {
						t.Fatal(err)
					}
					node := plan.CoercionExpr{Info: expr.Info{Usage: usage}, Child: plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: value}, Plan: conversion}
					group := &exec.FoldCaptureCandidate{}
					ctx := &exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: e.CatalogRead(), FoldCaptures: group}}
					if _, err := exec.FoldPlanningExpression(ctx, node); err != nil {
						t.Fatal(err)
					}
					if len(group.Entries) != 1 {
						t.Fatal("missing captured identity conversion")
					}
					backing := &group.Entries[0]
					group.Private = false
					if _, err := exec.FoldPlanningExpression(ctx, node); err != nil {
						t.Fatal(err)
					}
					if group.Private || backing != &group.Entries[0] {
						t.Error("unchanged physical input replaced retained backing")
					}
					group.Fixed = true
					if got, err := exec.FoldPlanningExpression(ctx, node); err != nil || !sqltype.SameDatum(got, value) {
						t.Error("fixed accepted physical input missed", err)
					}
					for j, otherWord := range tc.words {
						if i == j {
							continue
						}
						node.Child = plan.LiteralExpr{Info: expr.Info{Usage: usage}, Value: preparedFoldFloatWord(t, e, tc.id, otherWord)}
						if _, err := exec.FoldPlanningExpression(ctx, node); err != ids.ErrCatalogReplaced {
							t.Errorf("fixed generation accepted changed physical word %016x: %v", otherWord, err)
						}
					}
				})
			}
		})
	}
}

func TestPreparedFoldDatumInputBoundaries(t *testing.T) {
	e := engine.New()
	d, _ := e.CatalogRead().LookupType(sqltype.Float8)
	other, _ := engine.New().CatalogRead().LookupType(sqltype.Float8)
	usage := d.Usage()
	base := exec.FoldConstant{Result: usage, Value: sqltype.Null(d)}
	cases := []struct {
		name string
		input exec.FoldInput
	}{
		{"absent", exec.FoldInput{}},
		{"typed_null", exec.FoldInput{Usage: usage, Value: sqltype.Null(d)}},
		{"unknown_null", exec.FoldInput{Usage: usage, Value: sqltype.Null(d), UnknownNull: true}},
		{"native_descriptor", exec.FoldInput{Usage: other.Usage(), Value: sqltype.Null(other)}},
		{"typmod", exec.FoldInput{Usage: sqltype.Usage{Type: d, Typmod: 7}, Value: sqltype.Null(d)}},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := base
			a.Inputs = []exec.FoldInput{tc.input}
			if !sameLiteralFold(a, a) {
				t.Error("valid optional/NULL input not reflexive")
			}
			for j, other := range cases {
				if i == j {
					continue
				}
				b := base
				b.Inputs = []exec.FoldInput{other.input}
				if sameLiteralFold(a, b) {
					t.Errorf("input aliased %s", other.name)
				}
			}
			b := base
			b.Inputs = []exec.FoldInput{{}, tc.input}
			if sameLiteralFold(a, b) {
				t.Error("ordered omitted slot was erased")
			}
		})
	}
	invalid := base
	invalid.Inputs = []exec.FoldInput{{Usage: usage}}
	if sameLiteralFold(invalid, invalid) {
		t.Error("invalid literal inherited accepted datum identity")
	}
}

