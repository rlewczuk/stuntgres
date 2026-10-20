package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The normalized constructor is a source association only. Executable comparison
// fields retain their existing ownership and are never evaluated through it.
func (b *expressionBinder) normalizeQuantifiedConstructor(raw parser.Expr, constructor rowExpression) (expr.Expr, error) {
	members := make([]sqltype.CompositeMember, len(constructor.fields))
	for i, field := range constructor.fields {
		members[i] = constructor.Usage.Type.CompositeMember(i)
		members[i].Attribute = int32(i + 1)
		members[i].Usage = field.ResultUsage()
	}
	shape, err := sqltype.RecordShape(members)
	if err != nil {
		return nil, err
	}
	constructor.Usage = shape.Usage()
	if b.stored.syntax == nil {
		b.stored.syntax = make(map[storedExpressionSyntaxKey]parser.Expr)
	}
	b.stored.syntax[storedExpressionSyntaxKey{frame: b.frameID(), position: raw.SourcePosition()}] = *constructor.raw
	return constructor, nil
}

// Preserve the prepared source manifest. Only frozen presentation loses a
// constructor's erased outer identity, and only when no executable occurrence
// still needs that identity. The ordinary pg_depend expression owner decides.
func frozenQueryDependencies(view catalog.Read, b binding) ([]catalog.ExpressionDependency, error) {
	var erased map[sqltype.ID]bool
	visitCTEQuery(b.query.Root, func(LogicalNode) {}, func(bound expr.Expr) {
		walkBoundExpression(bound, func(node expr.Expr) {
			if n, ok := node.(subqueryExpression); ok && n.erasedLeftType != 0 {
				if erased == nil {
					erased = make(map[sqltype.ID]bool)
				}
				erased[n.erasedLeftType] = true
			}
		})
	}, true)
	if len(erased) == 0 {
		return b.dependencies, nil
	}
	refs := pgDependRefs{ctx: context.Background(), catalog: view}
	refs.query(b.query.Root)
	if refs.err != nil {
		return nil, refs.err
	}
	dependencies := make([]catalog.ExpressionDependency, 0, len(b.dependencies))
	for _, dependency := range b.dependencies {
		address := dependency.Ref.Address
		if address.ClassID == ids.TypeClassID && erased[sqltype.ID(address.ObjectID)] {
			if _, retained := refs.addresses[address]; !retained {
				continue
			}
		}
		dependencies = append(dependencies, dependency)
	}
	return dependencies, nil
}

// Quantified and residual IN constructors share source slots, expansion
// associations and synthetic member casts; only frozen capture needs this walk.
func (b *expressionBinder) captureConstructorFields(raw parser.RowExpr, count int, fieldAt func(int) expr.Expr, expansions map[int]compositeExpansionBinding, memberCasts bool, path viewLiteralPath, ordinal *int) error {
	if memberCasts && b.capture.castUsages == nil {
		b.capture.castUsages = make(map[viewLiteralPath]sqltype.Usage)
	}
	slot := 0
	for source, field := range raw.Fields {
		if expansion, ok := expansions[source]; ok {
			key := path
			key.node = *ordinal
			if slot+len(expansion.fields) > count {
				return fmt.Errorf("stuntgres: missing row comparison source association")
			}
			if memberCasts {
				for i := range expansion.fields {
					key.argument = i + 1
					b.capture.castUsages[key] = fieldAt(slot + i).ResultUsage()
				}
			}
			if err := b.captureCompositeExpansion(field, expansion, path, ordinal); err != nil {
				return err
			}
			slot += len(expansion.fields)
			continue
		}
		if slot >= count {
			return fmt.Errorf("stuntgres: missing row comparison source association")
		}
		if memberCasts {
			key := path
			key.argument = source + 1
			b.capture.castUsages[key] = fieldAt(slot).ResultUsage()
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

func (v *viewRenderer) quantifiedFieldsSQL(ctx context.Context, catalog catalog.Read, path viewLiteralPath, fields []string) (string, error) {
	for i, text := range fields {
		key := path
		key.argument = i + 1
		usage, captured := v.castUsages[key]
		if !captured {
			continue
		}
		if v.presentation.deparse {
			r := pgRenderer{view: catalog, path: v.presentation.path, pretty: v.presentation.pretty, output: v.presentation.output}
			var err error
			fields[i], err = r.cast(text, usage, false)
			if err != nil {
				return "", err
			}
		} else {
			declaration, err := catalogTypeDeclaration(catalog, usage)
			if err != nil {
				return "", err
			}
			fields[i] = "CAST(" + text + " AS " + declaration + ")"
		}
	}
	return strings.Join(fields, ", "), nil
}
