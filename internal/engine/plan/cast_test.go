package plan

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// Only codec environments are needed by DatumCastExpr; an unexpected runtime
// capability call fails rather than providing a substitute conversion.
type castCodecContext struct{ expr.Context }

func (castCodecContext) FunctionContext() context.Context { return context.Background() }
func (castCodecContext) FunctionInput() sqltype.InputEnvironment {
	return sqltype.InputEnvironment{}
}
func (castCodecContext) FunctionOutput() sqltype.OutputEnvironment {
	return sqltype.OutputEnvironment{}
}

type castDatumOperand struct {
	expr.Info
	value sqltype.Value
}

func (n castDatumOperand) Eval(expr.Context) (sqltype.Value, error) { return n.value, nil }

func castUnitTypes() map[sqltype.ID]*sqltype.Descriptor {
	types := make(map[sqltype.ID]*sqltype.Descriptor)
	for _, d := range sqltype.Builtins() {
		types[d.ID()] = d
	}
	return types
}

func TestCastDefinitionIOTypmodSizing(t *testing.T) {
	types := castUnitTypes()
	ctx := castCodecContext{}
	for _, id := range []sqltype.ID{sqltype.VarChar, sqltype.BPChar} {
		for _, tc := range []struct {
			name, text, want string
			typmod           int32
			mode             sqltype.CoercionContext
			null, rejected   bool
		}{
			{"explicit", "red", "re", 6, sqltype.Explicit, false, false},
			{"multibyte", "éclair", "é", 5, sqltype.Explicit, false, false},
			{"assignment", "red", "", 6, sqltype.Assignment, false, true},
			{"unconstrained", "red", "red", -1, sqltype.Assignment, false, false},
			{"null", "", "", 6, sqltype.Assignment, true, false},
		} {
			t.Run(types[id].Name()+"/"+tc.name, func(t *testing.T) {
				value := sqltype.String(types[sqltype.Text], tc.text)
				if tc.null {
					value = sqltype.Null(types[sqltype.Text])
				}
				child := castDatumOperand{Info: expr.Info{Usage: value.Type().Usage()}, value: value}
				target := types[id].Usage()
				target.Typmod = tc.typmod
				for _, method := range []string{"i", "b"} {
					n, err := NewDatumCast(expr.Info{Usage: target}, child, method, tc.mode)
					if err != nil {
						t.Fatal(err)
					}
					for _, rebuild := range []bool{false, true} {
						if rebuild {
							retained, err := n.Rebuild(n.Info, n.Children())
							if err != nil {
								t.Fatal(err)
							}
							n = retained.(DatumCastExpr)
						}
						got, err := n.Eval(ctx)
						if tc.rejected {
							var diagnostic interface{ SQLState() string }
							if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "22001" {
								t.Fatalf("%s rebuild=%v: want 22001, got %v", method, rebuild, err)
							}
							continue
						}
						if err != nil || got.Type() != target.Type || got.IsNull() != tc.null {
							t.Fatalf("%s rebuild=%v: result %v, error %v", method, rebuild, got, err)
						}
						if !tc.null {
							text, err := target.Type.Output(context.Background(), got)
							if err != nil || text != tc.want {
								t.Fatalf("%s rebuild=%v: got %q, %v; want %q", method, rebuild, text, err, tc.want)
							}
						}
					}
					// Rebuilding a formerly constrained node must discard sizing.
					info := n.Info
					info.Usage.Typmod = -1
					rebuilt, err := n.Rebuild(info, n.Children())
					if err != nil || rebuilt.(DatumCastExpr).Size || !rebuilt.(DatumCastExpr).Sizing.Equal(sqltype.CoercionPlan{}) {
						t.Fatalf("obsolete sizing retained: %v", err)
					}
				}
			})
		}
	}
}

func TestCastDefinitionIODescriptorVolatility(t *testing.T) {
	types := castUnitTypes()
	enum, err := sqltype.Enum(60001, "cast_enum", "public.cast_enum", []string{"red"})
	if err != nil {
		t.Fatal(err)
	}
	composite, err := sqltype.Composite(60002, "cast_record", "public.cast_record", 0, []sqltype.CompositeMember{{Attribute: 1, Name: "v", Usage: types[sqltype.Date].Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		typ                       *sqltype.Descriptor
		inputStable, outputStable bool
	}{
		{enum, true, true},
		{composite, true, true},
		{types[sqltype.Int4Range], true, true},
		{types[sqltype.Int4Multirange], true, true},
		{types[1007], true, true},
		{types[sqltype.Xid], false, false},
		{types[sqltype.Xid8], false, false},
		{types[sqltype.Float4], false, true},
	} {
		typ := tc.typ
		for _, input := range []bool{false, true} {
			source, target := typ, types[sqltype.Text]
			stable := tc.outputStable
			if input {
				source, target, stable = target, source, tc.inputStable
			}
			child := LiteralExpr{Info: expr.Info{Usage: source.Usage()}, Value: sqltype.Null(source)}
			n, err := NewDatumCast(expr.Info{Usage: target.Usage()}, child, "i", sqltype.Explicit)
			if err != nil {
				t.Fatal(err)
			}
			want := expr.Immutable
			if stable {
				want = expr.Stable
			}
			if n.IntrinsicVolatility() != want || n.Properties().Volatility != want || GenerationImmutable(n) == stable {
				t.Fatalf("%s input=%v: volatility=%v, generation=%v", typ.Name(), input, n.Properties(), GenerationImmutable(n))
			}
			volatile := castDatumOperand{Info: expr.Info{Usage: source.Usage(), Property: expr.Properties{Volatility: expr.Volatile, RowDependent: true}}, value: child.Value}
			rebuilt, err := n.Rebuild(expr.Info{Usage: target.Usage()}, []expr.Expr{volatile})
			if err != nil || rebuilt.Properties() != volatile.Properties() || GenerationImmutable(rebuilt) {
				t.Fatalf("child effects lost on rebuild: %v, %v", rebuilt, err)
			}
			if err := n.ValidateStoredProvider(); err != nil {
				t.Fatal(err)
			}
		}
	}
	child := LiteralExpr{Info: expr.Info{Usage: types[sqltype.Xid].Usage()}, Value: sqltype.Null(types[sqltype.Xid])}
	immutable, err := NewDatumCast(expr.Info{Usage: types[sqltype.Float4].Usage()}, child, "i", sqltype.Explicit)
	if err != nil || immutable.IntrinsicVolatility() != expr.Immutable || !GenerationImmutable(immutable) {
		t.Fatalf("XID output/REAL input must remain immutable: %v", err)
	}
}
