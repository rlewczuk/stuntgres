package engine

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
)

type alterTypeIndex struct {
	index *relationDescriptor
	keys  []int
	sql   string
}

func captureAlterTypeIndexes(scope *StatementScope, catalog CatalogRead, original, survivor *relationDescriptor, actions []parser.AlterTableAction) ([]alterTypeIndex, error) {
	op := scope.op
	hasType := false
	for _, action := range actions {
		hasType = hasType || action.Action == parser.AlterColumnType
	}
	if !hasType {
		return nil, nil
	}
	var queue []alterTypeIndex
	for _, descriptor := range catalog.state.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.sqlIndex.parent != original.address {
			continue
		}
		pending := alterTypeIndex{index: index}
		dropped := false
		for _, key := range index.sqlIndex.keys {
			if key.derived == nil {
				dropped = dropped || !survivor.hasAttribute(key.attribute)
			} else {
				for _, dep := range key.derived.dependencies {
					a := dep.Address
					if a.ClassID == original.address.ClassID && a.ObjectID == original.address.ObjectID && a.SubID != 0 && !survivor.hasAttribute(a.SubID) {
						dropped = true
					}
				}
			}
		}
		if dropped {
			continue
		}
		for i, key := range index.sqlIndex.keys {
			if key.derived == nil {
				continue
			}
			affected := false
			for _, action := range actions {
				if action.Action != parser.AlterColumnType {
					continue
				}
				slot, exists := original.index[action.Column.Text]
				if !exists {
					continue
				}
				for _, dep := range key.derived.dependencies {
					a := dep.Address
					if a.ClassID == original.address.ClassID && a.ObjectID == original.address.ObjectID && a.SubID == original.attributes[slot] {
						affected = true
					}
				}
			}
			if !affected {
				continue
			}
			pending.keys = append(pending.keys, i)
		}
		if len(pending.keys) > 0 {
			var sql strings.Builder
			sql.WriteString("CREATE INDEX " + pgIdentifier(index.name) + " ON " + pgIdentifier(catalog.state.descriptors[original.namespace].Name()) + "." + pgIdentifier(original.name) + " USING btree (")
			renderer := pgRenderer{view: catalog, relation: original, path: scope.deparsePath, output: scope.output}
			for i, key := range index.sqlIndex.keys {
				if i > 0 {
					sql.WriteString(", ")
				}
				if key.derived != nil {
					text, err := renderer.sql(op.Context(), key.derived.definition, pgRenderParent{})
					if err != nil {
						return nil, err
					}
					if indexFunctionExpression(key.derived.definition) {
						sql.WriteString(text)
					} else {
						sql.WriteString("(" + text + ")")
					}
				} else {
					for slot, attribute := range original.attributes {
						if attribute == key.attribute {
							sql.WriteString(pgIdentifier(original.columns[slot].Name))
							break
						}
					}
				}
				if key.desc {
					sql.WriteString(" DESC")
				}
				if key.nullsFirst && !key.desc {
					sql.WriteString(" NULLS FIRST")
				} else if !key.nullsFirst && key.desc {
					sql.WriteString(" NULLS LAST")
				}
			}
			sql.WriteByte(')')
			pending.sql = sql.String()
			queue = append(queue, pending)
		}
	}
	return queue, nil
}

func (e *Engine) rebindAlterTypeIndexes(scope *StatementScope, auth AuthorizationView, parent *relationDescriptor, stage *CatalogStage, queue []alterTypeIndex) error {
	for _, pending := range queue {
		copy := *pending.index
		definition := *copy.sqlIndex
		definition.keys = append([]indexKey(nil), definition.keys...)
		copy.sqlIndex = &definition
		syntax, err := parser.ParseWithOptions(pending.sql, parser.Options{Context: scope.op.Context(), StandardConformingStrings: true})
		if err != nil {
			return err
		}
		for _, slot := range pending.keys {
			e.mu.Lock()
			derived, _, err := e.bindIndexExpression(scope.op, scope.input, auth, parent, syntax[0].(*parser.CreateIndex).Keys[slot].Expression, nil)
			e.mu.Unlock()
			if err != nil {
				return err
			}
			definition.keys[slot].derived = derived
		}
		next, err := evolveIndex(&copy, parent)
		if err != nil {
			return err
		}
		if next != nil {
			if next.revision == pending.index.revision {
				next.revision++
				if next.revision == 0 {
					return fmt.Errorf("stuntgres: index revision exhausted")
				}
			}
			if err = stage.Replace(next); err != nil {
				return err
			}
		}
	}
	return nil
}

func indexFunctionExpression(node BoundExpr) bool {
	switch n := node.(type) {
	case stringExpression:
		return n.plan.Name() != "||"
	case mathExpression, binaryExpression, randomExpression, nullCountExpression, sequenceCallExpression, currentSchemasExpression, visibilityInquiryExpression, commentInquiryExpression, deparseExpression:
		return true
	default:
		return false
	}
}
