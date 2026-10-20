package analyze

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The normalized constructor is a source association only. Executable comparison
// fields retain their existing ownership and are never evaluated through it.
func (b *Binder) normalizeQuantifiedConstructor(raw parser.Expr, constructor plan.RowExpr) (expr.Expr, error) {
	members := make([]sqltype.CompositeMember, len(constructor.Fields))
	for i, field := range constructor.Fields {
		members[i] = constructor.Usage.Type.CompositeMember(i)
		members[i].Attribute = int32(i + 1)
		members[i].Usage = field.ResultUsage()
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	constructor.Usage = shape.Usage()
	if b.Stored.Syntax == nil {
		b.Stored.Syntax = make(map[StoredExpressionSyntaxKey]parser.Expr)
	}
	b.Stored.Syntax[StoredExpressionSyntaxKey{Frame: b.FrameID(), Position: raw.SourcePosition()}] = *constructor.Raw
	return constructor, nil
}

// Quantified and residual IN constructors share source slots, expansion
// associations and synthetic member casts; only frozen capture needs this walk.
func (b *Binder) captureConstructorFields(raw parser.RowExpr, count int, fieldAt func(int) expr.Expr, expansions map[int]plan.CompositeExpansionBinding, memberCasts bool, path ViewLiteralPath, ordinal *int) error {
	if memberCasts && b.Capture.CastUsages == nil {
		b.Capture.CastUsages = make(map[ViewLiteralPath]sqltype.Usage)
	}
	slot := 0
	for source, field := range raw.Fields {
		if expansion, ok := expansions[source]; ok {
			key := path
			key.Node = *ordinal
			if slot+len(expansion.Fields) > count {
				return fmt.Errorf("stuntgres: missing row comparison source association")
			}
			if memberCasts {
				for i := range expansion.Fields {
					key.Argument = i + 1
					b.Capture.CastUsages[key] = fieldAt(slot + i).ResultUsage()
				}
			}
			if err := b.captureCompositeExpansion(field, expansion, path, ordinal); err != nil {
				return err
			}
			slot += len(expansion.Fields)
			continue
		}
		if slot >= count {
			return fmt.Errorf("stuntgres: missing row comparison source association")
		}
		if memberCasts {
			key := path
			key.Argument = source + 1
			b.Capture.CastUsages[key] = fieldAt(slot).ResultUsage()
		}
		if err := b.captureNormalizedViewLiteralTree(field, fieldAt(slot), path, ordinal); err != nil {
			return err
		}
		slot++
	}
	if slot != count {
		return fmt.Errorf("stuntgres: invalid row comparison source association")
	}
	return nil
}
