package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Graph addresses are immutable identities, not definition revision pins.
type inheritanceOrigin struct {
	parent     ObjectAddress
	attribute  int32
	constraint OID
}
type inheritedColumn struct {
	local              bool
	explicitDefault    bool
	explicitGeneration bool
	origins            []inheritanceOrigin
}
type inheritedConstraint struct {
	local   bool
	origins []inheritanceOrigin
	retired []retiredInheritanceOrigin
}
type tableInheritance struct {
	parents []ObjectAddress
	columns map[int32]inheritedColumn
	checks  map[string]inheritedConstraint
	notNull map[int32]inheritedConstraint
}
type createInheritanceInput struct {
	ref    ObjectRef
	source *relationDescriptor
}

func inheritanceHasChildren(v CatalogRead, parent ObjectAddress) bool {
	for _, descriptor := range v.state.descriptors {
		if d, ok := descriptor.(*relationDescriptor); ok && d.inheritance != nil && slices.Contains(d.inheritance.parents, parent) {
			return true
		}
	}
	return false
}

func inheritanceChildren(v CatalogRead, parent ObjectAddress) []*relationDescriptor {
	var children []*relationDescriptor
	for _, descriptor := range v.state.descriptors {
		d, ok := descriptor.(*relationDescriptor)
		if ok && d.inheritance != nil && slices.Contains(d.inheritance.parents, parent) {
			children = append(children, d)
		}
	}
	slices.SortFunc(children, func(a, b *relationDescriptor) int {
		if a.address.ObjectID < b.address.ObjectID {
			return -1
		}
		if a.address.ObjectID > b.address.ObjectID {
			return 1
		}
		return 0
	})
	return children
}
func inheritanceParticipant(v CatalogRead, d *relationDescriptor) bool {
	return d.inheritance != nil || inheritanceHasChildren(v, d.address)
}

func inheritanceTypeName(u sqltype.Usage) string {
	name := u.Type.DiagnosticName()
	if u.Typmod != u.Type.Usage().Typmod {
		declaration, _ := u.Declaration()
		name += strings.TrimPrefix(declaration, u.Type.SQLName())
	}
	return name
}
func inheritanceColumnConflict(view CatalogRead, old, next ColumnDescriptor, local bool) error {
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
		d.Detail = fmt.Sprintf(`"%s" versus "%s"`, collationName(view, old.Usage.Collation.ID), collationName(view, next.Usage.Collation.ID))
		return d
	}
	return nil
}

// Local duplicate detection precedes merging; LIKE remains a local copy, not an edge.
func (e *Engine) describeInheritance(op *Operation, auth AuthorizationView, syntax *parser.CreateTable, b *binding, columns []ColumnDescriptor, declarations []createColumnDeclaration, likeConstraints []createLikeConstraint) ([]ColumnDescriptor, []createColumnDeclaration, []createLikeConstraint, error) {
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
	var merged []ColumnDescriptor
	b.createLocalColumns = localNames
	index := make(map[string]int)
	seen := make(map[ObjectAddress]bool)
	generated := make(map[string]bool)
	kinds := make(map[string]generationKind)
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
		source := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
		if source.relationKind == compositeRelation {
			d := sqlError("42809", fmt.Sprintf(`cannot open relation "%s"`, source.name)).(*sqlstate.Error)
			d.Detail = "This operation is not supported for composite types."
			return nil, nil, nil, d
		}
		if source.partition != nil {
			return nil, nil, nil, sqlError("42809", fmt.Sprintf(`cannot inherit from partitioned table "%s"`, source.name))
		}
		if source.membership != nil {
			return nil, nil, nil, sqlError("42809", fmt.Sprintf(`cannot inherit from partition "%s"`, source.name))
		}
		if source.relationKind == foreignRelation {
			return nil, nil, nil, sqlError("0A000", "foreign table hierarchies are unsupported in embedded mode")
		}
		if source.relationKind != tableRelation {
			return nil, nil, nil, sqlError("42809", fmt.Sprintf(`inherited relation "%s" is not a table or foreign table`, source.name))
		}
		if err := auth.Check(AccessRequirement{Object: ref, OwnerOnly: true}); err != nil {
			return nil, nil, nil, err
		}
		parentNS := auth.catalog.state.descriptors[source.namespace].(*namespaceDescriptor)
		if parentNS.temporary != (SessionToken{}) && syntax.Persistence != "temporary" && auth.stagedTemporary == (ObjectAddress{}) {
			return nil, nil, nil, sqlError("42809", fmt.Sprintf(`cannot inherit from temporary relation "%s"`, source.name))
		}
		if seen[ref.Address] {
			return nil, nil, nil, sqlError("42P07", fmt.Sprintf(`relation "%s" would be inherited from more than once`, source.name))
		}
		seen[ref.Address] = true
		b.createInheritance = append(b.createInheritance, createInheritanceInput{ref, source})
		for sourceSlot, c := range source.columns {
			definition, isGenerated := source.generations[source.attributes[sourceSlot]]
			if slot, ok := index[c.Name]; ok {
				op.emitNotice(levelNotice, sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf(`merging multiple inherited definitions of column "%s"`, c.Name)})
				if err := inheritanceColumnConflict(auth.catalog, merged[slot], c, false); err != nil {
					return nil, nil, nil, err
				}
				if generated[c.Name] != isGenerated {
					return nil, nil, nil, sqlError("42804", fmt.Sprintf(`inherited column "%s" has a generation conflict`, c.Name))
				}
				if isGenerated && kinds[c.Name] != definition.kind {
					return nil, nil, nil, generationInheritanceError(c.Name, kinds[c.Name], definition.kind)
				}
			} else {
				index[c.Name] = len(merged)
				generated[c.Name] = isGenerated
				kinds[c.Name] = definition.kind
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
			if err := inheritanceColumnConflict(auth.catalog, merged[slot], c, true); err != nil {
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
			kind := generationVirtual
			if declarations[i].generation.Kind == "stored" {
				kind = generationStored
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
	for i := range b.createLikes {
		in := &b.createLikes[i]
		in.targetSlots = make([]int, len(in.source.columns))
		for j := range in.targetSlots {
			in.targetSlots[j] = mapping[in.offset+j]
		}
	}
	for _, in := range b.createLikes {
		if in.options&likeGenerated != 0 {
			for i, attribute := range in.source.attributes {
				if _, exists := in.source.generations[attribute]; exists {
					name := in.source.columns[i].Name
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
		for i, attribute := range in.source.attributes {
			_, specified := in.source.defaults[attribute]
			if in.source.inheritance != nil {
				specified = specified || in.source.inheritance.columns[attribute].explicitDefault
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
