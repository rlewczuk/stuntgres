package analyze

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// castAnnotation is shared by operand classification and final cast binding.
// Classification must preserve the same retained identity as the selected cast.
func (b *Binder) castAnnotation(raw parser.CastExpr) (parser.TypeName, error) {
	if b.Clause.JSONBRecordBase {
		return b.JSONBRecordCastAnnotation(raw)
	}
	return b.TypeCastAnnotation(raw)
}

// TypeCastAnnotation reconstructs a held explicit user-type occurrence from
// its admitted logical identity. Normal annotation lookup, authority and coercion
// still run. Environment/definition invalidation retires this occurrence group.
func (b *Binder) TypeCastAnnotation(raw parser.CastExpr) (parser.TypeName, error) {
	annotation := raw.TypeName
	if !b.Expected.Held || raw.Syntax == parser.FunctionCast {
		return annotation, nil
	}
	view := b.BindingCatalog()
	if b.Expected.CatalogOwner != view.Owner() || b.Expected.CatalogEpoch != view.Epoch() {
		return annotation, nil
	}
	key := PreparedTypeKey{Offset: raw.TypeName.Pos.Offset, Kind: DomainCast}
	captured, found := b.Expected.Types[key]
	if !found {
		key.Kind = IdentityCast
		captured, found = b.Expected.Types[key]
	}
	if !found {
		return annotation, nil
	}
	if captured.Kind != key.Kind || captured.Base.Type == nil {
		return annotation, fmt.Errorf("stuntgres: incompatible retained type occurrence")
	}
	original := captured.Base.Type
	domain := original.IsDomain() || original.IsArray() && original.Element().IsDomain()
	if key.Kind == DomainCast && !domain || key.Kind == IdentityCast && (domain || !original.IsUserDefined() && !(original.IsArray() && original.Element().IsUserDefined())) {
		return annotation, fmt.Errorf("stuntgres: incompatible retained type occurrence")
	}
	current, exists := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(original.ID())})
	if !exists {
		return annotation, nil
	}
	if original.IsArray() {
		if !current.SQLType.IsArray() || current.SQLType.Element().ID() != original.Element().ID() {
			return annotation, nil
		}
		element, exists := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(original.Element().ID())})
		if !exists || element.Array != original.ID() || element.SQLType != current.SQLType.Element() || element.Namespace() != current.Namespace() {
			return annotation, nil
		}
		// Written d[] bounds name its current element. Direct _d annotations
		// name the actual current companion, including displaced spellings.
		if len(annotation.ArrayBounds) != 0 {
			current = element
		}
	} else if original.IsDomain() != current.SQLType.IsDomain() {
		return annotation, nil
	}
	namespace := view.Get(current.Namespace())
	if namespace == nil {
		return annotation, fmt.Errorf("stuntgres: missing retained type namespace")
	}
	annotation.Name, annotation.Quoted = current.Name(), true
	annotation.Schema = &parser.Identifier{Text: namespace.Name(), Pos: raw.TypeName.Pos}
	return annotation, nil
}
