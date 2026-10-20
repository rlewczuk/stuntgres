package engine

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type createInheritanceInput struct {
	ref    ids.ObjectRef
	source *catalog.Relation
}

func inheritanceHasChildren(v catalog.Read, parent ids.ObjectAddress) bool {
	for _, descriptor := range v.Descriptors() {
		if d, ok := descriptor.(*catalog.Relation); ok && d.Inheritance != nil && slices.Contains(d.Inheritance.Parents, parent) {
			return true
		}
	}
	return false
}

func inheritanceChildren(v catalog.Read, parent ids.ObjectAddress) []*catalog.Relation {
	var children []*catalog.Relation
	for address := range v.Descriptors() {
		if address.ClassID != ids.RelationClassID {
			continue
		}
		d, ok := v.Relation(address)
		if ok && d.Inheritance != nil && slices.Contains(d.Inheritance.Parents, parent) {
			children = append(children, d)
		}
	}
	slices.SortFunc(children, func(a, b *catalog.Relation) int {
		if a.Address().ObjectID < b.Address().ObjectID {
			return -1
		}
		if a.Address().ObjectID > b.Address().ObjectID {
			return 1
		}
		return 0
	})
	return children
}
func inheritanceParticipant(v catalog.Read, d *catalog.Relation) bool {
	return d.Inheritance != nil || inheritanceHasChildren(v, d.Address())
}

func inheritanceTypeName(u sqltype.Usage) string {
	name := u.Type.DiagnosticName()
	if u.Typmod != u.Type.Usage().Typmod {
		declaration, _ := u.Declaration()
		name += strings.TrimPrefix(declaration, u.Type.SQLName())
	}
	return name
}
func inheritanceColumnConflict(view catalog.Read, old, next catalog.Column, local bool) error {
	prefix := "inherited column"
	if local {
		prefix = "column"
	}
	if old.Usage.Type != next.Usage.Type || old.Usage.Typmod != next.Usage.Typmod {
		d := sqlError("42804", fmt.Sprintf(`%s "%s" has a type conflict`, prefix, old.Name)).(*sqlstate.Error)
		d.Detail = inheritanceTypeName(old.Usage) + " versus " + inheritanceTypeName(next.Usage)
		return d
	}
	if old.Usage.Collation.ID != next.Usage.Collation.ID {
		d := sqlError("42P21", fmt.Sprintf(`%s "%s" has a collation conflict`, prefix, old.Name)).(*sqlstate.Error)
		d.Detail = fmt.Sprintf(`"%s" versus "%s"`, typedef.CollationName(view, old.Usage.Collation.ID), typedef.CollationName(view, next.Usage.Collation.ID))
		return d
	}
	return nil
}

// Local duplicate detection precedes merging; LIKE remains a local copy, not an edge.
func (e *Engine) describeInheritance(op *Operation, auth AuthorizationView, syntax *parser.CreateTable, b *binding, columns []catalog.Column, declarations []createColumnDeclaration, likeConstraints []createLikeConstraint) ([]catalog.Column, []createColumnDeclaration, []createLikeConstraint, error) {
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
	b.create.localColumns = localNames
	index := make(map[string]int)
	seen := make(map[ids.ObjectAddress]bool)
	generated := make(map[string]bool)
	kinds := make(map[string]catalog.GenerationKind)
	for _, name := range syntax.Parents {
		ref, err := e.resolveReference(auth, name)
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok {
				copy := *d
				copy.Position = ""
				err = &copy
			}
			return nil, nil, nil, err
		}
		source := auth.Catalog.MustRelation(ref.Address)
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
		if err := auth.Check(access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
			return nil, nil, nil, err
		}
		parentNS := auth.Catalog.MustNamespace(source.Namespace())
		if parentNS.Temporary != (ids.SessionToken{}) && syntax.Persistence != "temporary" && auth.stagedTemporary == (ids.ObjectAddress{}) {
			return nil, nil, nil, sqlError("42809", fmt.Sprintf(`cannot inherit from temporary relation "%s"`, source.Name()))
		}
		if seen[ref.Address] {
			return nil, nil, nil, sqlError("42P07", fmt.Sprintf(`relation "%s" would be inherited from more than once`, source.Name()))
		}
		seen[ref.Address] = true
		b.create.inheritance = append(b.create.inheritance, createInheritanceInput{ref, source})
		for sourceSlot, c := range source.Columns {
			definition, isGenerated := source.ColumnMeta.Generations[source.Attributes[sourceSlot]]
			if slot, ok := index[c.Name]; ok {
				op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`merging multiple inherited definitions of column "%s"`, c.Name)})
				if err := inheritanceColumnConflict(auth.Catalog, merged[slot], c, false); err != nil {
					return nil, nil, nil, err
				}
				if generated[c.Name] != isGenerated {
					return nil, nil, nil, sqlError("42804", fmt.Sprintf(`inherited column "%s" has a generation conflict`, c.Name))
				}
				if isGenerated && kinds[c.Name] != definition.Kind {
					return nil, nil, nil, generationInheritanceError(c.Name, kinds[c.Name], definition.Kind)
				}
			} else {
				index[c.Name] = len(merged)
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
	resultDeclarations := make([]createColumnDeclaration, len(merged))
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
			op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: message, Detail: detail})
			if err := inheritanceColumnConflict(auth.Catalog, merged[slot], c, true); err != nil {
				return nil, nil, nil, err
			}
		} else {
			slot = len(merged)
			index[c.Name] = slot
			merged = append(merged, c)
			resultDeclarations = append(resultDeclarations, createColumnDeclaration{})
		}
		if generated[c.Name] && (declarations[i].defaultExpr != nil || declarations[i].serial) {
			return nil, nil, nil, sqlError("42611", fmt.Sprintf(`column "%s" inherits from generated column but specifies default`, c.Name))
		}
		if ok && !generated[c.Name] && declarations[i].generation != nil {
			diagnostic := sqlError("42611", fmt.Sprintf(`child column "%s" specifies generation expression`, c.Name)).(*sqlstate.Error)
			diagnostic.Hint = "A child table column cannot be generated unless its parent column is."
			return nil, nil, nil, diagnostic
		}
		if ok && generated[c.Name] && declarations[i].generation != nil {
			kind := catalog.GenerationVirtual
			if declarations[i].generation.Kind == "stored" {
				kind = catalog.GenerationStored
			}
			if kinds[c.Name] != kind {
				return nil, nil, nil, generationInheritanceError(c.Name, kinds[c.Name], kind)
			}
		}
		mapping[i] = slot
		resultDeclarations[slot] = declarations[i]
	}
	for i := range likeConstraints {
		likeConstraints[i].Attribute = int32(mapping[likeConstraints[i].Attribute-1] + 1)
	}
	for i := range b.create.likes {
		in := &b.create.likes[i]
		in.targetSlots = make([]int, len(in.source.Columns))
		for j := range in.targetSlots {
			in.targetSlots[j] = mapping[in.offset+j]
		}
	}
	for _, in := range b.create.likes {
		if in.options&likeGenerated != 0 {
			for i, attribute := range in.source.Attributes {
				if _, exists := in.source.ColumnMeta.Generations[attribute]; exists {
					name := in.source.Columns[i].Name
					if isGenerated, inherited := generated[name]; inherited && !isGenerated {
						diagnostic := sqlError("42611", fmt.Sprintf(`child column "%s" specifies generation expression`, name)).(*sqlstate.Error)
						diagnostic.Hint = "A child table column cannot be generated unless its parent column is."
						return nil, nil, nil, diagnostic
					}
					resultDeclarations[in.targetSlot(i)].copiedGeneration = true
				}
			}
		}
		if in.options&likeDefaults == 0 {
			continue
		}
		for i, attribute := range in.source.Attributes {
			_, specified := in.source.ColumnMeta.Defaults[attribute]
			if in.source.Inheritance != nil {
				specified = specified || in.source.Inheritance.Columns[attribute].ExplicitDefault
			}
			if specified {
				resultDeclarations[in.targetSlot(i)].copiedDefault = true
			}
		}
	}
	for slot, column := range merged {
		if generated[column.Name] && resultDeclarations[slot].copiedDefault {
			return nil, nil, nil, sqlError("42611", fmt.Sprintf(`column "%s" inherits from generated column but specifies default`, column.Name))
		}
	}
	return merged, resultDeclarations, likeConstraints, nil
}
