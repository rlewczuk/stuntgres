package regobj

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

type float4DumpKey struct{}
type constructorBoundsKey struct{}

func ConstructorBoundedInputContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, constructorBoundsKey{}, true)
}
func ConstructorArrayTargetBounds(v sqltype.Value) string {
	if v.IsNull() || !v.Type().BaseType().IsArray() || !(ContainsCustomRange(v.Type()) || v.RequiresCircleConstructorReplay() || NeedsFloat4RawReplay(v) && v.Type().BaseType().Element().BaseType().ID() != sqltype.Float4) {
		return ""
	}
	needed := false
	for i := range v.ArrayRank() {
		needed = needed || v.ArrayDimension(i).Lower != 1
	}
	if !needed {
		return ""
	}
	var out strings.Builder
	for i := range v.ArrayRank() {
		d := v.ArrayDimension(i)
		out.WriteString("[" + strconv.FormatInt(int64(d.Lower), 10) + ":" + strconv.FormatInt(int64(d.Lower)+int64(d.Length)-1, 10) + "]")
	}
	return out.String()
}
func Float4DumpContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, float4DumpKey{}, true)
}
func ExactFloat4Dump(ctx context.Context) bool {
	enabled, _ := ctx.Value(float4DumpKey{}).(bool)
	return enabled
}
func ContainsFloat4(t *sqltype.Descriptor) bool {
	if t.IsDomain() {
		return ContainsFloat4(t.BaseType())
	}
	if t.ID() == sqltype.Float4 {
		return true
	}
	if r := t.RangeType(); r != nil {
		if d, ok := r.RangeDefinition(); ok {
			return ContainsFloat4(d.Subtype.Type)
		}
	}
	if t.IsArray() {
		return ContainsFloat4(t.Element())
	}
	if t.IsComposite() {
		for i := range t.CompositeLen() {
			if ContainsFloat4(t.CompositeMember(i).Usage.Type) {
				return true
			}
		}
	}
	return false
}

// NeedsFloat4RawReplay applies the scalar word test to actual container leaves,
// not their declared types. Ordinary values keep their canonical SQL rendering.
func NeedsFloat4RawReplay(v sqltype.Value) bool {
	if v.IsNull() || v.Type() == nil || !ContainsFloat4(v.Type()) {
		return false
	}
	t := v.Type().BaseType()
	if t.ID() == sqltype.Float4 {
		return v.Float4NeedsRawReplay()
	}
	if t.IsRange() {
		bounds, err := v.RangeBounds()
		if err != nil || bounds.Empty {
			return false
		}
		return NeedsFloat4RawReplay(bounds.Lower) || NeedsFloat4RawReplay(bounds.Upper)
	}
	if t.IsMultirange() {
		for i := range v.MultirangeLen() {
			if NeedsFloat4RawReplay(v.MultirangeMember(i)) {
				return true
			}
		}
		return false
	}
	if t.IsArray() {
		for i := range v.ArrayLen() {
			if NeedsFloat4RawReplay(v.ArrayElement(i)) {
				return true
			}
		}
	}
	if t.IsComposite() {
		for i := range v.CompositeLen() {
			if NeedsFloat4RawReplay(v.CompositeField(i)) {
				return true
			}
		}
	}
	return false
}
func constructorLiteral(ctx context.Context, view catalog.Read, reader catalog.DefinitionReader, n plan.LiteralExpr, acl sqltype.ACLItemFormatter, bounded bool) (string, error) {
	u, v := n.Usage, n.Value
	declaration, err := TypeDeclarationAt(view, reader, u)
	if err != nil {
		return "", err
	}
	if v.IsNull() {
		return "CAST(NULL AS " + declaration + ")", nil
	}
	if u.Type.ID() == sqltype.Float4 && ExactFloat4Dump(ctx) && v.Float4NeedsRawReplay() {
		bits, err := v.Float4Bits()
		if err != nil {
			return "", err
		}
		return "CAST(CAST('" + strconv.FormatUint(uint64(bits), 10) + "' AS pg_catalog.xid) AS pg_catalog.float4)", nil
	}
	if u.Type.IsRange() || u.Type.IsMultirange() {
		return rangeConstructorLiteral(ctx, view, reader, n, acl)
	}
	if u.Type.IsComposite() {
		parts := make([]string, v.CompositeLen())
		for i := range parts {
			parts[i], err = literalWithACLAt(ctx, view, reader, plan.LiteralExpr{Info: expr.Info{Usage: u.Type.CompositeMember(i).Usage}, Value: v.CompositeField(i)}, acl, false)
			if err != nil {
				return "", err
			}
		}
		return "CAST(ROW(" + strings.Join(parts, ", ") + ") AS " + declaration + ")", nil
	}
	if u.Type.IsArray() {
		if u.Type.Element().BaseType().ID() == sqltype.Float4 && ExactFloat4Dump(ctx) && NeedsFloat4RawReplay(v) {
			if u.Type.Element().ID() == sqltype.Float4 {
				declaration = "pg_catalog.float4[]"
			}
			var text strings.Builder
			for i := range v.ArrayRank() {
				dim := v.ArrayDimension(i)
				text.WriteString("[" + strconv.FormatInt(int64(dim.Lower), 10) + ":" + strconv.FormatInt(int64(dim.Lower)+int64(dim.Length)-1, 10) + "]")
			}
			if v.ArrayRank() > 0 {
				text.WriteByte('=')
			}
			at := 0
			var writeDimension func(int) error
			writeDimension = func(depth int) error {
				text.WriteByte('{')
				if depth < v.ArrayRank() {
					for i := range int(v.ArrayDimension(depth).Length) {
						if i > 0 {
							text.WriteByte(',')
						}
						if depth+1 < v.ArrayRank() {
							if err := writeDimension(depth + 1); err != nil {
								return err
							}
						} else {
							leaf := v.ArrayElement(at)
							at++
							if leaf.IsNull() {
								text.WriteString("NULL")
							} else {
								if leaf.Type().IsDomain() {
									var e error
									leaf, e = leaf.BaseValue()
									if e != nil {
										return e
									}
								}
								bits, e := leaf.Float4Bits()
								if e != nil {
									return e
								}
								text.WriteString(strconv.FormatUint(uint64(bits), 10))
							}
						}
					}
				}
				text.WriteByte('}')
				return nil
			}
			if err := writeDimension(0); err != nil {
				return "", err
			}
			return "CAST(CAST('" + text.String() + "' AS pg_catalog._xid) AS " + declaration + ")", nil
		}
		at := 0
		var dimension func(int) (string, error)
		dimension = func(depth int) (string, error) {
			parts := make([]string, int(v.ArrayDimension(depth).Length))
			for i := range parts {
				if depth+1 < v.ArrayRank() {
					parts[i], err = dimension(depth + 1)
				} else {
					parts[i], err = literalWithACLAt(ctx, view, reader, plan.LiteralExpr{Info: expr.Info{Usage: u.Type.Element().Usage()}, Value: v.ArrayElement(at)}, acl, false)
					at++
				}
				if err != nil {
					return "", err
				}
			}
			return "ARRAY[" + strings.Join(parts, ", ") + "]", nil
		}
		if v.ArrayLen() == 0 {
			return "CAST(ARRAY[] AS " + declaration + ")", nil
		}
		if ConstructorArrayTargetBounds(v) != "" {
			if !bounded {
				if v.ArrayRank() != 1 {
					return "", fmt.Errorf("stuntgres: multidimensional constructor literals require a bounded INSERT target")
				}
				first, e := literalWithACLAt(ctx, view, reader, plan.LiteralExpr{Info: expr.Info{Usage: u.Type.Element().Usage()}, Value: v.ArrayElement(0)}, acl, false)
				if e != nil {
					return "", e
				}
				seed := "pg_catalog.array_fill(" + first + ", ARRAY[1], ARRAY[" + strconv.FormatInt(int64(v.ArrayDimension(0).Lower), 10) + "])"
				if v.ArrayLen() == 1 {
					return seed, nil
				}
				rest := make([]string, v.ArrayLen()-1)
				for i := range rest {
					rest[i], e = literalWithACLAt(ctx, view, reader, plan.LiteralExpr{Info: expr.Info{Usage: u.Type.Element().Usage()}, Value: v.ArrayElement(i + 1)}, acl, false)
					if e != nil {
						return "", e
					}
				}
				return "pg_catalog.array_cat(" + seed + ", CAST(ARRAY[" + strings.Join(rest, ", ") + "] AS " + declaration + "))", nil
			}
		}
		text, err := dimension(0)
		return "CAST(" + text + " AS " + declaration + ")", err
	}
	return "", fmt.Errorf("stuntgres: invalid constructor container")
}

// ContainsCustomRange selects the ordinary recursive constructor renderer for
// nested custom families; their endpoints may carry symbolic or exact leaves.
func ContainsCustomRange(t *sqltype.Descriptor) bool {
	t = t.BaseType()
	if r := t.RangeType(); r != nil {
		_, custom := r.RangeDefinition()
		return custom
	}
	if t.IsArray() {
		return ContainsCustomRange(t.Element())
	}
	if t.IsComposite() {
		for i := range t.CompositeLen() {
			if ContainsCustomRange(t.CompositeMember(i).Usage.Type) {
				return true
			}
		}
	}
	return false
}
