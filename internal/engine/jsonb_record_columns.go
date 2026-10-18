package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) FunctionJSONBRecordInput() sqltype.InputEnvironment {
	input := c.Input
	input.DomainChecks = jsonbRecordDomainChecks{c}
	return input
}

// PostgreSQL saves a domain's own constraint violation, not errors raised while
// executing its CHECK expression. Keep that distinction across nested text
// composite/array input as well as JSON-node conversion.
type jsonbRecordDomainChecks struct{ evaluation *EvalContext }

type jsonbRecordHardDomainError struct{ error }

func (e jsonbRecordHardDomainError) Unwrap() error { return e.error }

func (c jsonbRecordDomainChecks) CheckDomain(ctx context.Context, target *sqltype.Descriptor, value sqltype.Value) error {
	err := c.evaluation.CheckDomain(ctx, target, value)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) && (diagnostic.Code == "23514" || diagnostic.Code == "23502") {
		view := c.evaluation.domainCatalog()
		if view.state != nil {
			d, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.ID())})
			if ok && d.SQLType == target && diagnostic.DataType == d.Name() && diagnostic.Schema == view.Get(d.ObjectNamespace).Name() {
				if diagnostic.Code == "23502" && diagnostic.Constraint == "" {
					return err
				}
				// Inherited checks report the requested domain, but retain their
				// declaring constraint name. Read only the captured generation.
				for current := d; ; {
					for _, check := range current.DomainChecks {
						if diagnostic.Constraint == check.Name {
							return err
						}
					}
					base := current.SQLType.DomainBase().Type
					if !base.IsDomain() {
						break
					}
					parent, found := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.ID())})
					if !found || parent.SQLType != base {
						break
					}
					current = parent
				}
			}
		}
	}
	// Soft-error classification deliberately examines the direct error, whereas
	// ordinary SQL diagnostics and cancellation follow this unchanged cause.
	return jsonbRecordHardDomainError{err}
}

// Only the analyzed projection is frozen. The function source keeps its current
// complete schema and still converts every destination member before yielding.
func (b *expressionBinder) jsonbRecordStarColumns(columns []queryColumn, frame uint32, star parser.StarExpr, qualified *queryQualifier) ([]queryColumn, error) {
	if qualified != nil {
		return b.jsonbRecordQualifierStar(columns, frame, star, *qualified)
	}
	if b.queryNamespace == nil {
		return columns, nil
	}
	for _, qualifier := range b.queryNamespace.qualifiers {
		var err error
		columns, err = b.jsonbRecordQualifierStar(columns, frame, star, qualifier)
		if err != nil {
			return nil, err
		}
	}
	return columns, nil
}

func (b *expressionBinder) jsonbRecordQualifierStar(columns []queryColumn, frame uint32, star parser.StarExpr, qualifier queryQualifier) ([]queryColumn, error) {
	source, ok := qualifier.sourceRoot.(*functionFromNode)
	if !ok || source.frame != frame {
		return columns, nil
	}
	offset := 0
	for _, expression := range source.calls {
		width := functionFromCallWidth(expression)
		if call, selected := expression.(functions.JSONBRecord); selected && call.ResultUsage().Type.BaseType().IsComposite() {
			if offset+width > len(qualifier.columns) {
				return nil, fmt.Errorf("stuntgres: invalid JSONB record source span")
			}
			var err error
			columns, err = b.jsonbRecordCallStar(columns, qualifier.columns[offset:offset+width], star, call)
			if err != nil {
				return nil, err
			}
		}
		offset += width
	}
	return columns, nil
}

func (b *expressionBinder) jsonbRecordCallStar(columns, source []queryColumn, star parser.StarExpr, call functions.JSONBRecord) ([]queryColumn, error) {
	usage := call.ResultUsage()
	shape := usage.Type.BaseType()
	key := preparedCompositeKey{offset: call.SourcePosition().Offset, kind: compositeSourceExpansion, projection: star.Pos.Offset}
	captured, retained, err := b.retainedComposite(key, usage)
	if err != nil {
		return nil, err
	}
	first := 0
	if len(source) != 0 {
		first = source[0].slot
	}
	if !retained {
		captured = compositeOccurrence{kind: compositeSourceExpansion, base: usage, attributes: make([]int32, 0, len(source)), names: make([]string, 0, len(source))}
		for _, column := range columns {
			index := column.slot - first
			if index >= 0 && index < len(source) {
				captured.attributes = append(captured.attributes, shape.CompositeMember(index).Attribute)
				captured.names = append(captured.names, column.descriptor.Name)
			}
		}
	}
	if len(captured.names) != len(captured.attributes) {
		return nil, fmt.Errorf("stuntgres: invalid retained JSONB record labels")
	}
	if b.composites == nil {
		b.composites = make(map[preparedCompositeKey]compositeOccurrence)
	}
	b.composites[key] = captured
	if !retained {
		return columns, nil
	}
	for _, attribute := range captured.attributes {
		if _, exists := shape.CompositeAttributeIndex(attribute); !exists {
			return nil, sqlError("42703", fmt.Sprintf("attribute %d of type record has been dropped", attribute))
		}
	}
	if len(source) == 0 {
		return columns, nil
	}
	count, changed := 0, false
	for _, column := range columns {
		index := column.slot - first
		if index >= 0 && index < len(source) {
			if count >= len(captured.attributes) || captured.attributes[count] != shape.CompositeMember(index).Attribute || captured.names[count] != column.descriptor.Name {
				changed = true
			}
			count++
		}
	}
	if count != len(captured.attributes) {
		changed = true
	}
	if !changed {
		return columns, nil
	}
	next := make([]queryColumn, 0, len(columns))
	emitted := false
	for _, column := range columns {
		index := column.slot - first
		if index < 0 || index >= len(source) {
			next = append(next, column)
			continue
		}
		if emitted {
			continue
		}
		for i, attribute := range captured.attributes {
			member, _ := shape.CompositeAttributeIndex(attribute)
			retainedColumn := source[member]
			retainedColumn.descriptor.Name = captured.names[i]
			next = append(next, retainedColumn)
		}
		emitted = true
	}
	return next, nil
}

// A retained base cast selects the current spelling of its original provider,
// then goes through ordinary annotation resolution and privilege checks. Never
// look up an old OID across a catalog owner/epoch boundary or retry a failed name.
func (b *expressionBinder) jsonbRecordCastAnnotation(raw parser.CastExpr) (parser.TypeName, error) {
	annotation := raw.TypeName
	if !b.jsonbRecordBase || raw.Syntax == parser.FunctionCast || b.expected == nil {
		return annotation, nil
	}
	view := b.bindingCatalog()
	if b.expected.catalogOwner != view.owner || b.expected.catalogEpoch != view.Epoch() {
		return annotation, nil
	}
	key := preparedCompositeKey{offset: raw.TypeName.Pos.Offset, kind: compositeRecordArgument}
	captured, found := b.expected.composites[key]
	if !found {
		return annotation, nil
	}
	if captured.kind != compositeRecordArgument || captured.base.Type == nil || !captured.base.Type.BaseType().IsComposite() {
		return annotation, fmt.Errorf("stuntgres: incompatible retained composite argument")
	}
	current, _ := view.LookupType(captured.base.Type.ID())
	usage := captured.base
	usage.Type = current
	if _, valid, err := b.retainedComposite(key, usage); err != nil || !valid {
		return annotation, err
	}
	descriptor, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(current.ID())})
	if !ok || descriptor.SQLType != current {
		return annotation, fmt.Errorf("stuntgres: incompatible retained composite argument")
	}
	namespace := view.Get(descriptor.ObjectNamespace)
	if namespace == nil {
		return annotation, fmt.Errorf("stuntgres: missing retained composite namespace")
	}
	annotation.Name, annotation.Quoted = descriptor.Name(), true
	annotation.Schema = &parser.Identifier{Text: namespace.Name(), Pos: raw.TypeName.Pos}
	return annotation, nil
}
