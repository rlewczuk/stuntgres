package engine

import "stuntgres/internal/sqltype"

func remapFrozenPresentation(view CatalogRead, original *viewDefinition, mapping map[ObjectAddress]ObjectAddress, ref func(ObjectRef) ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation collationRemapper) (*viewDefinition, error) {
	definition := *original
	if collation != nil && len(original.collations) != 0 {
		definition.collations = make(map[viewLiteralPath]uint32, len(original.collations))
		for path, id := range original.collations {
			if target, ok := mapping[ObjectAddress{ClassID: CollationClassID, ObjectID: OID(id)}]; ok {
				id = uint32(target.ObjectID)
			}
			definition.collations[path] = id
		}
	}
	definition.sources = append([]viewLeaf(nil), original.sources...)
	for i := range definition.sources {
		definition.sources[i].ref = ref(definition.sources[i].ref)
	}
	if len(original.literals) != 0 {
		definition.literals = make(map[viewLiteralPath]literalExpression, len(original.literals))
		for path, literal := range original.literals {
			node, _, err := retagBoundTypes(view, literal, typ, collation)
			if err != nil {
				return nil, err
			}
			node, err = remapBoundExpression(node, ref, nil)
			if err != nil {
				return nil, err
			}
			definition.literals[path] = node.(literalExpression)
		}
	}
	if len(original.castUsages) != 0 {
		definition.castUsages = make(map[viewLiteralPath]sqltype.Usage, len(original.castUsages))
		for path, usage := range original.castUsages {
			usage.Type = typ(usage.Type)
			if collation != nil {
				var err error
				usage.Collation, err = collation(usage.Collation)
				if err != nil {
					return nil, err
				}
			}
			definition.castUsages[path] = usage
		}
	}
	definition.sequenceRefs = make(map[int]ObjectRef, len(original.sequenceRefs))
	for offset, old := range original.sequenceRefs {
		definition.sequenceRefs[offset] = ref(old)
	}
	definition.dependencies = append([]expressionDependency(nil), original.dependencies...)
	for i := range definition.dependencies {
		definition.dependencies[i].ref = ref(definition.dependencies[i].ref)
	}
	root, err := remapViewQuery(view, original.query.Root, ref, typ, collation)
	if err != nil {
		return nil, err
	}
	query := *original.query
	query.Root = root
	definition.query = &query
	definition.frames, definition.queries = collectQueryFrames(root)
	if project, ok := root.(*projectNode); ok && project.syntax == nil && project.frame != 0 {
		definition.frames[project.frame] = project
	}
	return &definition, nil
}
