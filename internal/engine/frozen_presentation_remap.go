package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func remapFrozenPresentation(view CatalogRead, original *viewDefinition, mapping map[ids.ObjectAddress]ids.ObjectAddress, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation collationRemapper) (*viewDefinition, error) {
	typ = recordTypeMapper(typ, collation)
	definition := *original
	if len(original.columns) != 0 {
		definition.columns = make(map[viewLiteralPath]viewColumn, len(original.columns))
		for path, column := range original.columns {
			if column.ruleSide != 0 {
				column.ruleRef = ref(column.ruleRef)
			}
			definition.columns[path] = column
		}
	}
	if len(original.intaggRefs) != 0 {
		definition.intaggRefs = make(map[viewLiteralPath]ids.ObjectRef, len(original.intaggRefs))
		for path, old := range original.intaggRefs {
			definition.intaggRefs[path] = ref(old)
		}
	}
	if len(original.operatorRefs) != 0 {
		definition.operatorRefs = make(map[viewLiteralPath]ids.ObjectRef, len(original.operatorRefs))
		for path, old := range original.operatorRefs {
			definition.operatorRefs[path] = ref(old)
		}
	}
	if collation != nil && len(original.collations) != 0 {
		definition.collations = make(map[viewLiteralPath]uint32, len(original.collations))
		for path, id := range original.collations {
			if target, ok := mapping[ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(id)}]; ok {
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
	if len(original.composites) != 0 {
		definition.composites = make(map[viewLiteralPath]compositeOccurrence, len(original.composites))
		for path, captured := range original.composites {
			captured.base.Type = typ(captured.base.Type)
			if collation != nil {
				var err error
				captured.base.Collation, err = collation(captured.base.Collation)
				if err != nil {
					return nil, err
				}
			}
			definition.composites[path] = captured
		}
	}
	definition.sequenceRefs = make(map[int]ids.ObjectRef, len(original.sequenceRefs))
	for offset, old := range original.sequenceRefs {
		definition.sequenceRefs[offset] = ref(old)
	}
	definition.dependencies = append([]catalog.ExpressionDependency(nil), original.dependencies...)
	for i := range definition.dependencies {
		definition.dependencies[i].Ref = ref(definition.dependencies[i].Ref)
		if definition.dependencies[i].Ref.Address.ClassID == ids.ProcedureClassID {
			procedure, ok := view.Procedure(definition.dependencies[i].Ref.Address)
			if !ok {
				return nil, ids.ErrCatalogReplaced
			}
			definition.dependencies[i].Revision = procedure.Revision()
		}
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
