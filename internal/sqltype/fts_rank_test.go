package sqltype

import (
	"context"
	"errors"
	"math"
	"testing"
)

func TestFTSRankTypedPlan(t *testing.T) {
	types, foreign := testTypes(), testTypes()
	for _, density := range []bool{false, true} {
		for _, weighted := range []bool{false, true} {
			for _, normalized := range []bool{false, true} {
				op := FTSRankFunction{density, weighted, normalized}
				var usages [4]Usage
				var values [4]Value
				for i := range op.Arity() {
					id := op.Formal(i)
					usages[i] = types[id].Usage()
					input := "cat:1"
					switch id {
					case TSQuery:
						input = "cat"
					case 1021:
						input = "{0.1,0.2,0.4,1}"
					case Int4:
						input = "0"
					}
					v, err := types[id].Input(t.Context(), input, -1)
					if err != nil {
						t.Fatal(err)
					}
					values[i] = v
				}
				plan, err := BindFTSRank(op, types[Float4].Usage(), usages)
				if err != nil {
					t.Fatal(op, err)
				}
				value, err := plan.Evaluate(t.Context(), values)
				want := uint32(0x3d7901c7)
				if density {
					want = 0x3dcccccd
				}
				if err != nil || value.Type() != types[Float4] || math.Float32bits(float32(value.floating)) != want {
					t.Fatal(op, value, err)
				}
				for i := range op.Arity() {
					nulls := values
					nulls[i] = Null(usages[i].Type)
					value, err := plan.Evaluate(t.Context(), nulls)
					if err != nil || !value.null || value.typ != types[Float4] {
						t.Fatal("strict NULL", op, i, err)
					}
					bad := values
					bad[i] = Null(foreign[op.Formal(i)])
					if _, err := plan.Evaluate(t.Context(), bad); err == nil {
						t.Fatal("foreign typed NULL", i)
					}
					badUsage := usages
					badUsage[i] = foreign[op.Formal(i)].Usage()
					if _, err := BindFTSRank(op, types[Float4].Usage(), badUsage); err == nil {
						t.Fatal("foreign operand", i)
					}
				}
				if _, err := BindFTSRank(op, foreign[Float4].Usage(), usages); err == nil {
					t.Fatal("foreign result")
				}
				vectorIndex := 0
				if weighted {
					vectorIndex = 1
				}
				bad := values
				bad[vectorIndex] = Value{typ: types[TSVector]}
				if _, err := plan.Evaluate(t.Context(), bad); err == nil {
					t.Fatal("invalid vector payload")
				}
				bad = values
				bad[vectorIndex+1] = Value{typ: types[TSQuery]}
				if _, err := plan.Evaluate(t.Context(), bad); err == nil {
					t.Fatal("invalid query payload")
				}
				if weighted {
					bad = values
					bad[0] = Value{typ: types[1021]}
					if _, err := plan.Evaluate(t.Context(), bad); err == nil {
						t.Fatal("missing array payload")
					}
					array := *values[0].array
					array.elements = append([]Value(nil), array.elements...)
					array.elements[0] = Null(foreign[Float4])
					bad = values
					bad[0].array = &array
					if _, err := plan.Evaluate(t.Context(), bad); err == nil {
						t.Fatal("foreign array datum")
					}
					for _, dimensions := range [][]ArrayDimension{
						nil, {{Lower: 1, Length: 3}}, {{Lower: 1, Length: 0}}, {{Lower: 1, Length: -1}},
						{{Lower: 1<<31 - 1, Length: 2}}, make([]ArrayDimension, 7),
					} {
						array := *values[0].array
						array.dimensions = dimensions
						bad = values
						bad[0].array = &array
						if _, err := plan.Evaluate(t.Context(), bad); err == nil {
							t.Fatal("invalid physical rank array shape", dimensions)
						}
					}
					copyVector, copyArray := *types[TSVector], *types[1021]
					copyArray.element = foreign[Float4]
					copyArray.ftsVector = &copyVector
					copyVector.ftsRealArray = &copyArray
					badUsage := usages
					badUsage[0] = copyArray.Usage()
					badUsage[1] = copyVector.Usage()
					if _, err := BindFTSRank(op, types[Float4].Usage(), badUsage); err == nil {
						t.Fatal("foreign array descriptor element")
					}
				}
				ctx, cancel := context.WithCancel(t.Context())
				cancel()
				if _, err := plan.Evaluate(ctx, values); !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		}
	}
}
