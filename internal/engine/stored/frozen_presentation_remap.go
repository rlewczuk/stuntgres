package stored

import (
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

func RemapView(view catalog.Read, original *View, mapping map[ids.ObjectAddress]ids.ObjectAddress, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation typedef.CollationRemapper, procedure catalog.ProcedureRemapper) (*View, error) {
	typ = analyze.RecordTypeMapper(typ, collation)
	return remapView(view, original, mapping, ref, typ, collation, nil, procedure)
}

func remapView(view catalog.Read, original *View, mapping map[ids.ObjectAddress]ids.ObjectAddress, ref func(ids.ObjectRef) ids.ObjectRef, typ func(*sqltype.Descriptor) *sqltype.Descriptor, collation typedef.CollationRemapper, addressMapper func(ids.ObjectAddress) ids.ObjectAddress, procedure catalog.ProcedureRemapper) (*View, error) {
	definition := *original
	definition.routineInputs = slices.Clone(original.routineInputs)
	for i, usage := range definition.routineInputs {
		usage.Type = typ(usage.Type)
		if collation != nil {
			var err error
			usage.Collation, err = collation(usage.Collation)
			if err != nil {
				return nil, err
			}
		}
		definition.routineInputs[i] = usage
	}
	if len(original.columns) != 0 {
		definition.columns = make(map[analyze.ViewLiteralPath]analyze.ViewColumn, len(original.columns))
		for path, column := range original.columns {
			if column.TableOID {
				column.Owner = ref(column.Owner)
			}
			if column.RuleSide != 0 {
				column.RuleRef = ref(column.RuleRef)
				if relation, ok := view.Relation(ids.ObjectAddress{ClassID: column.RuleRef.Address.ClassID, ObjectID: column.RuleRef.Address.ObjectID}); ok {
					column.Slot = relation.IndexForAttribute(column.RuleRef.Address.SubID)
				}
			}
			definition.columns[path] = column
		}
	}
	if len(original.routineRefs) != 0 {
		definition.routineRefs = make(map[analyze.ViewLiteralPath]ids.ObjectRef, len(original.routineRefs))
		for path, old := range original.routineRefs {
			definition.routineRefs[path] = ref(old)
		}
	}
	if len(original.operatorRefs) != 0 {
		definition.operatorRefs = make(map[analyze.ViewLiteralPath]ids.ObjectRef, len(original.operatorRefs))
		for path, old := range original.operatorRefs {
			definition.operatorRefs[path] = ref(old)
		}
	}
	if collation != nil && len(original.collations) != 0 {
		definition.collations = make(map[analyze.ViewLiteralPath]uint32, len(original.collations))
		for path, id := range original.collations {
			if target, ok := mapping[ids.ObjectAddress{ClassID: ids.CollationClassID, ObjectID: ids.OID(id)}]; ok {
				id = uint32(target.ObjectID)
			}
			definition.collations[path] = id
		}
	}
	definition.sources = append([]ViewLeaf(nil), original.sources...)
	for i := range definition.sources {
		definition.sources[i].Ref = ref(definition.sources[i].Ref)
	}
	if len(original.literals) != 0 {
		definition.literals = make(map[analyze.ViewLiteralPath]plan.LiteralExpr, len(original.literals))
		for path, literal := range original.literals {
			mapper := &ViewQueryRemapper{Catalog: view, Type: typ, Collation: collation, Procedure: procedure}
			node, _, err := mapper.retag(literal, typ)
			if err != nil {
				return nil, err
			}
			node, err = remapScalar(node, ref, ref, nil, nil, 0, addressMapper)
			if err != nil {
				return nil, err
			}
			definition.literals[path] = node.(plan.LiteralExpr)
		}
	}
	if len(original.castUsages) != 0 {
		definition.castUsages = make(map[analyze.ViewLiteralPath]sqltype.Usage, len(original.castUsages))
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
		definition.composites = make(map[analyze.ViewLiteralPath]analyze.TypeOccurrence, len(original.composites))
		for path, captured := range original.composites {
			captured.Base.Type = typ(captured.Base.Type)
			if collation != nil {
				var err error
				captured.Base.Collation, err = collation(captured.Base.Collation)
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
	}
	mapper := &ViewQueryRemapper{Catalog: view, Ref: ref, Type: typ, Collation: collation, Procedure: originalProcedureMapper(view, mapping, procedure), routineFormals: len(original.routineInputs), addressMapper: addressMapper}
	var root plan.Node
	var err error
	if _, project := original.query.Root.(*plan.ProjectNode); project {
		root, err = mapper.Nested(original.query.Root)
	} else {
		root, err = mapper.Query(original.query.Root)
	}
	if err != nil {
		return nil, err
	}
	query := *original.query
	query.Root = root
	definition.query = &query
	definition.frames, definition.queries = analyze.CollectQueryFrames(root)
	if project, ok := root.(*plan.ProjectNode); ok && project.Syntax == nil && project.Frame != 0 {
		definition.frames[project.Frame] = project
	}
	return &definition, nil
}

// Without an explicit import policy only target-domain principals may survive.
// Numeric IDs are used to verify identity, never to fabricate a mapping.
func originalProcedureMapper(view catalog.Read, mapping map[ids.ObjectAddress]ids.ObjectAddress, supplied catalog.ProcedureRemapper) catalog.ProcedureRemapper {
	if supplied != nil {
		return supplied
	}
	principal := func(ref authorization.PrincipalRef) (authorization.PrincipalRef, error) {
		if view.Owner() == nil || view.Owner().Principals == nil || ref != view.Owner().Principals.Principal(ref.ID()) || ref.ID() == 0 {
			return authorization.PrincipalRef{}, ids.ErrCatalogReplaced
		}
		return ref, nil
	}
	policy := catalog.PrincipalRemap{Principal: principal, SeededOwner: func(ref authorization.PrincipalRef, _ bool) (authorization.PrincipalRef, error) {
		return principal(ref)
	}}
	return extension.ProcedureRemapper(extension.DescriptorRemapper(mapping), catalog.NewProcedureRemapper(policy), nil)
}
