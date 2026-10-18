package engine

import (
	"context"
	"strconv"
	"strings"
	"stuntgres/internal/sqltype"
)

func routineLiteralSQL(view CatalogRead, value sqltype.Value) (string, error) {
	id, err := value.ProcedureOID()
	if err != nil {
		return "", err
	}
	if _, known := routineIdentityByOID(view, OID(id)); id == 0 || !known {
		return "'" + strconv.FormatUint(uint64(id), 10) + "'::" + value.Type().BaseType().Name(), nil
	}
	text := routineSpelling(view, nil, OID(id), true, true)
	literal := "'" + strings.ReplaceAll(text, "'", "''") + "'::regprocedure"
	if value.Type().BaseType().ID() == sqltype.Regproc {
		literal += "::regproc"
	}
	return literal, nil
}

// Only real descriptors create expression ownership dependencies. Source-fixed
// lookup-only identities never invent providers or extension replay events.
func walkRoutineReferences(view CatalogRead, value sqltype.Value, record func(ObjectAddress) error) error {
	return walkRoutineReferencesAt(view, nil, value, record)
}

func walkRoutineReferencesAt(view CatalogRead, reader definitionReader, value sqltype.Value, record func(ObjectAddress) error) error {
	if value.IsNull() || !value.Type().ContainsRegproc() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := walkRoutineReferencesAt(view, reader, value.CompositeField(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := walkRoutineReferencesAt(view, reader, value.ArrayElement(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	id, err := value.ProcedureOID()
	if err != nil {
		return err
	}
	address := ObjectAddress{ClassID: ProcedureClassID, ObjectID: OID(id)}
	if _, exists := definitionDescriptorAt(view, reader, address); exists {
		return record(address)
	}
	if owner := routineDescriptorOwner(view, OID(id)); owner != nil {
		if _, exists := definitionDescriptorAt(view, reader, owner.address); exists {
			return record(owner.address)
		}
	}
	return nil
}

func (r scalarRenderer) recordRoutineReferences(view CatalogRead, value sqltype.Value) error {
	return walkRoutineReferencesAt(view, r.definition, value, func(address ObjectAddress) error {
		r.record(view.owner.domain.Ref(view.Epoch(), address))
		return nil
	})
}

func remapRoutineDatum(value sqltype.Value, destination func(OID) OID) (sqltype.Value, error) {
	if value.IsNull() || !value.Type().ContainsRegproc() {
		return value, nil
	}
	if value.Type().BaseType().IsComposite() {
		return value.MapComposite(value.Type(), func(member sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapRoutineDatum(member, destination)
		})
	}
	if value.Type().BaseType().IsArray() {
		return value.MapArray(value.Type(), func(element sqltype.Value, _ *sqltype.Descriptor) (sqltype.Value, error) {
			return remapRoutineDatum(element, destination)
		})
	}
	id, err := value.ProcedureOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	target := destination(OID(id))
	if target == OID(id) {
		return value, nil
	}
	physical, err := sqltype.RegprocValue(value.Type().BaseType(), uint32(target))
	if err != nil {
		return sqltype.Value{}, err
	}
	if value.Type().IsDomain() {
		return value.Type().Wrap(physical)
	}
	return physical, nil
}

// Container fragments substitute only routine identities, never arbitrary data
// text. Numeric identities are rendered at replay after qualified signature
// resolution, so overloaded names, shape, bounds and nested quoting round-trip.
type routineFragment struct {
	text       string
	expression bool
}

func routineContainerLiteral(ctx context.Context, view CatalogRead, value sqltype.Value, usage sqltype.Usage, acl sqltype.ACLItemFormatter) (string, error) {
	return routineContainerLiteralAt(ctx, view, nil, value, usage, acl)
}

func routineContainerLiteralAt(ctx context.Context, view CatalogRead, reader definitionReader, value sqltype.Value, usage sqltype.Usage, acl sqltype.ACLItemFormatter) (string, error) {
	fragments, err := routineContainerFragments(ctx, view, value, acl)
	if err != nil {
		return "", err
	}
	declaration, err := catalogTypeDeclarationAt(view, reader, usage)
	if err != nil {
		return "", err
	}
	var sql, literal strings.Builder
	flush := func() {
		if literal.Len() == 0 {
			return
		}
		if sql.Len() != 0 {
			sql.WriteString(" || ")
		}
		sql.WriteByte('\'')
		sql.WriteString(strings.ReplaceAll(literal.String(), "'", "''"))
		sql.WriteByte('\'')
		literal.Reset()
	}
	for _, fragment := range fragments {
		if !fragment.expression {
			literal.WriteString(fragment.text)
			continue
		}
		flush()
		if sql.Len() != 0 {
			sql.WriteString(" || ")
		}
		sql.WriteString(fragment.text)
	}
	flush()
	return "CAST((" + sql.String() + ") AS " + declaration + ")", nil
}

func quoteRoutineFragments(fragments []routineFragment) []routineFragment {
	result := make([]routineFragment, 0, len(fragments)+2)
	result = append(result, routineFragment{text: "\""})
	for _, fragment := range fragments {
		if !fragment.expression {
			fragment.text = strings.ReplaceAll(strings.ReplaceAll(fragment.text, "\\", "\\\\"), "\"", "\\\"")
		}
		result = append(result, fragment)
	}
	return append(result, routineFragment{text: "\""})
}

func routineContainerFragments(ctx context.Context, view CatalogRead, value sqltype.Value, acl sqltype.ACLItemFormatter) ([]routineFragment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if value.IsNull() {
		return nil, nil
	}
	physical := value.Type().BaseType()
	if physical.IsArray() {
		var result []routineFragment
		bounds := false
		for i := range value.ArrayRank() {
			bounds = bounds || value.ArrayDimension(i).Lower != 1
		}
		if bounds {
			var prefix strings.Builder
			for i := range value.ArrayRank() {
				dim := value.ArrayDimension(i)
				prefix.WriteString("[" + strconv.FormatInt(int64(dim.Lower), 10) + ":" + strconv.FormatInt(int64(dim.Lower)+int64(dim.Length)-1, 10) + "]")
			}
			prefix.WriteByte('=')
			result = append(result, routineFragment{text: prefix.String()})
		}
		at := 0
		var dimension func(int) error
		dimension = func(depth int) error {
			result = append(result, routineFragment{text: "{"})
			if depth < value.ArrayRank() {
				for i := range int(value.ArrayDimension(depth).Length) {
					if i != 0 {
						result = append(result, routineFragment{text: string(physical.Delimiter())})
					}
					if depth+1 < value.ArrayRank() {
						if err := dimension(depth + 1); err != nil {
							return err
						}
					} else {
						element := value.ArrayElement(at)
						at++
						if element.IsNull() {
							result = append(result, routineFragment{text: "NULL"})
							continue
						}
						child, err := routineContainerFragments(ctx, view, element, acl)
						if err != nil {
							return err
						}
						result = append(result, quoteRoutineFragments(child)...)
					}
				}
			}
			result = append(result, routineFragment{text: "}"})
			return nil
		}
		if err := dimension(0); err != nil {
			return nil, err
		}
		return result, nil
	}
	if physical.IsComposite() {
		result := []routineFragment{{text: "("}}
		for i := range value.CompositeLen() {
			if i != 0 {
				result = append(result, routineFragment{text: ","})
			}
			field := value.CompositeField(i)
			if field.IsNull() {
				continue
			}
			child, err := routineContainerFragments(ctx, view, field, acl)
			if err != nil {
				return nil, err
			}
			result = append(result, quoteRoutineFragments(child)...)
		}
		return append(result, routineFragment{text: ")"}), nil
	}
	if sqltype.IsRoutineAlias(physical.ID()) {
		literal, err := routineLiteralSQL(view, value)
		if err != nil {
			return nil, err
		}
		return []routineFragment{{text: "(" + literal + ")::oid::text", expression: true}}, nil
	}
	if sqltype.IsOperatorAlias(physical.ID()) {
		literal, err := operatorLiteralSQL(view, value)
		if err != nil {
			return nil, err
		}
		return []routineFragment{{text: "(" + literal + ")::oid::text", expression: true}}, nil
	}
	text, err := value.Type().OutputWithEnvironment(ctx, value, sqltype.OutputEnvironment{Regclass: catalogRegclassOutput{view: view}, Regtype: dumpCatalogAliasOutput{view: view}, Regcollation: dumpCatalogAliasOutput{view: view}, Regnamespace: dumpCatalogAliasOutput{view: view}, Regrole: regroleContextFormatter(ctx), Operator: dumpOperatorOutput{}, RegTS: dumpRegTSOutput{view: view}, ACLItem: acl})
	if err != nil {
		return nil, err
	}
	return []routineFragment{{text: text}}, nil
}
