package analyze

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Local duplicate detection precedes merging; LIKE remains a local copy, not an edge.
func describeInheritance(host Host, auth Authority, syntax *parser.CreateTable, b *Binding, columns []catalog.Column, declarations []ColumnDeclaration, likeConstraints []LikeConstraint) ([]catalog.Column, []ColumnDeclaration, []LikeConstraint, error) {
	if len(syntax.Parents) == 0 {
		return columns, declarations, likeConstraints, nil
	}
	localNames := make(map[string]bool, len(columns))
	for _, c := range columns {
		if localNames[c.Name] {
			return nil, nil, nil, sqlError("42701", fmt.Sprintf(`column "%s" specified more than once`, c.Name))
		}
		localNames[c.Name] = true
	}
	var merged []catalog.Column
	b.Create.LocalColumns = localNames
	b.Create.InheritedStorage = make(map[string]catalog.ColumnStorage)
	index := make(map[string]int)
	seen := make(map[ids.ObjectAddress]bool)
	generated := make(map[string]bool)
	kinds := make(map[string]catalog.GenerationKind)
	for _, name := range syntax.Parents {
		ref, err := host.ResolveReference(auth, name)
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok {
				copy := *d
				copy.Position = ""
				err = &copy
			}
			return nil, nil, nil, err
		}
		source := auth.ReadCatalog().MustRelation(ref.Address)
		if source.RelationKind == catalog.CompositeRelation {
			d := sqlError("42809", fmt.Sprintf(`cannot open relation "%s"`, source.Name())).(*sqlstate.Error)
			d.Detail = "This operation is not supported for composite types."
			return nil, nil, nil, d
		}
		if source.Partition != nil {
			return nil, nil, nil, sqlError("42809", fmt.Sprintf(`cannot inherit from partitioned table "%s"`, source.Name()))
		}
		if source.Membership != nil {
			return nil, nil, nil, sqlError("42809", fmt.Sprintf(`cannot inherit from partition "%s"`, source.Name()))
		}
		if source.RelationKind == catalog.ForeignRelation {
			return nil, nil, nil, sqlError("0A000", "foreign table hierarchies are unsupported in embedded mode")
		}
		if source.RelationKind != catalog.TableRelation {
			return nil, nil, nil, sqlError("42809", fmt.Sprintf(`inherited relation "%s" is not a table or foreign table`, source.Name()))
		}
		if err := host.CheckPrivilege(auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return nil, nil, nil, err
		}
		parentNS := auth.ReadCatalog().MustNamespace(source.Namespace())
		if parentNS.Temporary != (ids.SessionToken{}) && syntax.Persistence != "temporary" && host.StagedTemporary(auth) == (ids.ObjectAddress{}) {
			return nil, nil, nil, sqlError("42809", fmt.Sprintf(`cannot inherit from temporary relation "%s"`, source.Name()))
		}
		if seen[ref.Address] {
			return nil, nil, nil, sqlError("42P07", fmt.Sprintf(`relation "%s" would be inherited from more than once`, source.Name()))
		}
		seen[ref.Address] = true
		b.Create.Inheritance = append(b.Create.Inheritance, InheritanceInput{Ref: ref, Source: source})
		for sourceSlot, c := range source.Columns {
			definition, isGenerated := source.ColumnMeta.Generations[source.Attributes[sourceSlot]]
			if slot, ok := index[c.Name]; ok {
				host.EmitNotice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`merging multiple inherited definitions of column "%s"`, c.Name)})
				if err := InheritanceColumnConflict(auth.ReadCatalog(), merged[slot], c, false); err != nil {
					return nil, nil, nil, err
				}
				properties, err := MergeInheritedColumnStorage(c.Name, b.Create.InheritedStorage[c.Name], source.ColumnStorage(sourceSlot))
				if err != nil {
					return nil, nil, nil, err
				}
				b.Create.InheritedStorage[c.Name] = properties
				if generated[c.Name] != isGenerated {
					return nil, nil, nil, sqlError("42804", fmt.Sprintf(`inherited column "%s" has a generation conflict`, c.Name))
				}
				if isGenerated && kinds[c.Name] != definition.Kind {
					return nil, nil, nil, GenerationInheritanceError(c.Name, kinds[c.Name], definition.Kind)
				}
			} else {
				index[c.Name] = len(merged)
				b.Create.InheritedStorage[c.Name] = source.ColumnStorage(sourceSlot)
				generated[c.Name] = isGenerated
				kinds[c.Name] = definition.Kind
				c.Nullable = true
				c.OriginTable = 0
				c.OriginAttribute = 0
				c.DeclaredDimensions = 0
				merged = append(merged, c)
			}
		}
	}
	resultDeclarations := make([]ColumnDeclaration, len(merged))
	mapping := make([]int, len(columns))
	for i, c := range columns {
		slot, ok := index[c.Name]
		if ok {
			message := fmt.Sprintf(`merging column "%s" with inherited definition`, c.Name)
			detail := ""
			if slot != i {
				message = fmt.Sprintf(`moving and merging column "%s" with inherited definition`, c.Name)
				detail = "User-specified column moved to the position of the inherited column."
			}
			host.EmitNotice(sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
			if err := InheritanceColumnConflict(auth.ReadCatalog(), merged[slot], c, true); err != nil {
				return nil, nil, nil, err
			}
			local := catalog.ColumnStorage{Storage: declarations[i].Storage, Compression: declarations[i].Compression}
			properties, err := MergeLocalColumnStorage(c.Name, b.Create.InheritedStorage[c.Name], local)
			if err != nil {
				return nil, nil, nil, err
			}
			b.Create.InheritedStorage[c.Name] = properties
		} else {
			slot = len(merged)
			index[c.Name] = slot
			merged = append(merged, c)
			resultDeclarations = append(resultDeclarations, ColumnDeclaration{})
		}
		if generated[c.Name] && (declarations[i].DefaultExpr != nil || declarations[i].Serial) {
			return nil, nil, nil, sqlError("42611", fmt.Sprintf(`column "%s" inherits from generated column but specifies default`, c.Name))
		}
		if ok && !generated[c.Name] && declarations[i].Generation != nil {
			diagnostic := sqlError("42611", fmt.Sprintf(`child column "%s" specifies generation expression`, c.Name)).(*sqlstate.Error)
			diagnostic.Hint = "A child table column cannot be generated unless its parent column is."
			return nil, nil, nil, diagnostic
		}
		if ok && generated[c.Name] && declarations[i].Generation != nil {
			kind := catalog.GenerationVirtual
			if declarations[i].Generation.Kind == "stored" {
				kind = catalog.GenerationStored
			}
			if kinds[c.Name] != kind {
				return nil, nil, nil, GenerationInheritanceError(c.Name, kinds[c.Name], kind)
			}
		}
		mapping[i] = slot
		resultDeclarations[slot] = declarations[i]
	}
	for i := range likeConstraints {
		likeConstraints[i].Attribute = int32(mapping[likeConstraints[i].Attribute-1] + 1)
	}
	for i := range b.Create.Likes {
		in := &b.Create.Likes[i]
		in.TargetSlots = make([]int, len(in.Source.Columns))
		for j := range in.TargetSlots {
			in.TargetSlots[j] = mapping[in.Offset+j]
		}
	}
	for _, in := range b.Create.Likes {
		if in.Options&LikeGenerated != 0 {
			for i, attribute := range in.Source.Attributes {
				if _, exists := in.Source.ColumnMeta.Generations[attribute]; exists {
					name := in.Source.Columns[i].Name
					if isGenerated, inherited := generated[name]; inherited && !isGenerated {
						diagnostic := sqlError("42611", fmt.Sprintf(`child column "%s" specifies generation expression`, name)).(*sqlstate.Error)
						diagnostic.Hint = "A child table column cannot be generated unless its parent column is."
						return nil, nil, nil, diagnostic
					}
					resultDeclarations[in.TargetSlot(i)].CopiedGeneration = true
				}
			}
		}
		if in.Options&LikeDefaults == 0 {
			continue
		}
		for i, attribute := range in.Source.Attributes {
			_, specified := in.Source.ColumnMeta.Defaults[attribute]
			if in.Source.Inheritance != nil {
				specified = specified || in.Source.Inheritance.Columns[attribute].ExplicitDefault
			}
			if specified {
				resultDeclarations[in.TargetSlot(i)].CopiedDefault = true
			}
		}
	}
	for slot, column := range merged {
		if generated[column.Name] && resultDeclarations[slot].CopiedDefault {
			return nil, nil, nil, sqlError("42611", fmt.Sprintf(`column "%s" inherits from generated column but specifies default`, column.Name))
		}
	}
	return merged, resultDeclarations, likeConstraints, nil
}
