package analyze

import (
	"fmt"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

// Domain identity is retained only by the ordinary held-analysis capture group.
// The executor removes these occurrences before environment/definition replan.
// Reconstruction still runs normal annotation lookup, authority and coercion.
func (b *Binder) DomainCastAnnotation(raw parser.CastExpr) (parser.TypeName, error) {
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
		return annotation, nil
	}
	if captured.Kind != DomainCast || captured.Base.Type == nil {
		return annotation, fmt.Errorf("stuntgres: incompatible retained domain occurrence")
	}
	original := captured.Base.Type
	if !original.IsDomain() && !(original.IsArray() && original.Element().IsDomain()) {
		return annotation, fmt.Errorf("stuntgres: incompatible retained domain occurrence")
	}
	current, exists := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(original.ID())})
	if !exists {
		return annotation, nil
	}
	if original.IsDomain() {
		if !current.SQLType.IsDomain() {
			return annotation, nil
		}
	} else {
		if !current.SQLType.IsArray() || !current.SQLType.Element().IsDomain() || current.SQLType.Element().ID() != original.Element().ID() {
			return annotation, nil
		}
		element, exists := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(original.Element().ID())})
		if !exists || element.Array != original.ID() || element.SQLType != current.SQLType.Element() || element.Namespace() != current.Namespace() {
			return annotation, nil
		}
		// d[] retains its written bounds and must name the current element,
		// whereas a direct _d annotation must name the current array itself.
		if len(annotation.ArrayBounds) != 0 {
			current = element
		}
	}
	namespace := view.Get(current.Namespace())
	if namespace == nil {
		return annotation, fmt.Errorf("stuntgres: missing retained domain namespace")
	}
	annotation.Name, annotation.Quoted = current.Name(), true
	annotation.Schema = &parser.Identifier{Text: namespace.Name(), Pos: raw.TypeName.Pos}
	return annotation, nil
}
