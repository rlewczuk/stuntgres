package typedef

import (
	"maps"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// ReownAddedColumnFill transports one added-column fill onto the declared type
// of the column it materializes. present is false when the relation or attribute
// is gone; the caller drops the fill, matching MergeFills and the projection,
// which only consume fills for live attributes.
func ReownAddedColumnFill(next *catalog.State, relation *catalog.Relation, attribute int32, value sqltype.Value) (sqltype.Value, bool, error) {
	if relation == nil || value.Type() == nil {
		return sqltype.Value{}, false, nil
	}
	slot := relation.IndexForAttribute(attribute)
	if slot < 0 {
		return sqltype.Value{}, false, nil
	}
	target := relation.Columns[slot].Usage.Type
	if target == nil {
		return sqltype.Value{}, false, nil
	}
	if value.Type() == target {
		return value, true, nil
	}
	imports := next.ScalarImports()
	if value.Type().ID() != target.ID() && enumReplacement(value.Type(), target) {
		imports = maps.Clone(imports)
		if imports == nil {
			imports = make(map[*sqltype.Descriptor]*sqltype.Descriptor, 2)
		}
		source, destination := value.Type().BaseType(), target.BaseType()
		imports[source] = destination
		if source.IsArray() {
			imports[source.Element()] = destination.Element()
		}
	}
	reowned, err := ReownDomainDatum(value, target, nil, imports, next.ACLItemImport())
	if err == nil {
		return reowned, true, nil
	}
	// A NULL fill carries no payload: restoring just its declared type keeps the
	// projection's pointer-identity invariant. Domains stay strict so their
	// NOT NULL and CHECK constraints are never bypassed.
	if value.IsNull() && target.Concrete() && !target.IsDomain() {
		return sqltype.Null(target), true, nil
	}
	return sqltype.Value{}, false, err
}

// enumReplacement reports whether source and target are the same array or
// scalar shape over different enum identities.
func enumReplacement(source, target *sqltype.Descriptor) bool {
	s, t := source.BaseType(), target.BaseType()
	if s.IsArray() != t.IsArray() {
		return false
	}
	if s.IsArray() {
		s, t = s.Element(), t.Element()
	}
	return s.IsEnum() && t.IsEnum()
}