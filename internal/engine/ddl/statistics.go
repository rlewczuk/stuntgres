package ddl

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type StatisticsHost interface {
	Host
	StatisticsCreationNamespace(Authority, parser.ObjectName) (ids.ObjectRef, Authority, error)
	StatisticsSchemaNamespace(Authority, parser.ObjectName) (ids.ObjectRef, Authority, error)
	ResolveRelation(Authority, parser.ObjectName) (ids.ObjectRef, error)
	AddDomainBase(catalog.Descriptor)
	BindStatisticsExpression(Authority, *catalog.Relation, parser.Expr, sqltype.InputEnvironment) (catalog.StatisticsExpression, error)
	SameStatisticsExpression(catalog.StatisticsExpression, catalog.StatisticsExpression) bool
}

func StageCreateStatistics(h StatisticsHost, auth Authority, s *parser.CreateStatistics, input sqltype.InputEnvironment) error {
	ref, err := h.ResolveRelation(auth, s.Relation)
	if err != nil {
		return err
	}
	view := h.AccessView(auth)
	rel, ok := view.Catalog.Relation(ref.Address)
	if !ok {
		return fmt.Errorf("stuntgres: missing statistics input relation")
	}
	if !catalog.StatisticsRelationKind(rel.RelationKind) {
		kind := catalog.RelationDiagnosticKind(rel) + "s"
		if rel.RelationKind == catalog.IndexRelation {
			kind = "indexes"
		}
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot define statistics for relation "%s"`, rel.Name()), Detail: "This operation is not supported for " + kind + "."}
	}
	if err = Check(h, auth, access.Requirement{Object: ref, OwnerOnly: true}); err != nil {
		return err
	}
	var ns ids.ObjectRef
	if len(s.Name.Parts) == 0 {
		ns = view.Owner.Domain.Ref(view.Catalog.Epoch(), rel.Namespace())
	} else {
		ns, auth, err = h.StatisticsCreationNamespace(auth, s.Name)
		if err != nil {
			return err
		}
	}
	if err = h.CheckCreateIn(auth, ns); err != nil {
		return err
	}
	object := ""
	if len(s.Name.Parts) != 0 {
		object = s.Name.Parts[len(s.Name.Parts)-1].Text
	}
	key := ids.NameKey{ClassID: ids.StatisticExtClassID, Namespace: ns.Address, Name: object}
	// PostgreSQL transforms written expressions before CREATE's duplicate
	// admission. Retain those bindings once; semantic dimension/kind checks
	// below still run only when an object will actually be created.
	bound := make([]catalog.StatisticsExpression, len(s.Elements))
	for i, element := range s.Elements {
		if col, simple := element.(parser.ColumnExpr); simple && len(col.Name.Parts) == 1 {
			continue
		}
		bound[i], err = h.BindStatisticsExpression(auth, rel, element, input)
		if err != nil {
			return err
		}
	}
	if object != "" && h.Stage().LookupName(key) != (ids.ObjectAddress{}) {
		message := fmt.Sprintf(`statistics object "%s" already exists`, object)
		if s.IfNotExists {
			Notice(h, "42710", message+", skipping")
			return nil
		}
		return sqlError("42710", message)
	}
	if len(s.Elements) > 8 {
		return sqlError("54011", "cannot have more than 8 columns in statistics")
	}
	keys := make([]int32, 0, len(s.Elements))
	names := make([]string, 0, len(s.Elements))
	expressions := make([]catalog.StatisticsExpression, 0, len(s.Elements))
	for elementIndex, element := range s.Elements {
		if col, simple := element.(parser.ColumnExpr); simple {
			name := col.Name.Parts[len(col.Name.Parts)-1].Text
			names = append(names, name)
			slot, exists := rel.Index[name]
			if !exists {
				for _, system := range []string{"tableoid", "ctid", "xmin", "xmax", "cmin", "cmax"} {
					if name == system {
						return sqlError("0A000", "statistics creation on system columns is not supported")
					}
				}
				return sqlError("42703", fmt.Sprintf(`column "%s" does not exist`, name))
			}
			attribute := rel.Attributes[slot]
			if rel.ColumnMeta.Generations[attribute].Kind == catalog.GenerationVirtual {
				return sqlError("0A000", "statistics creation on virtual generated columns is not supported")
			}
			typ := rel.Columns[slot].Usage.Type
			if _, ok := view.Catalog.UniqueDefaultIndexClass(403, typ); !ok {
				return sqlError("0A000", fmt.Sprintf(`column "%s" cannot be used in statistics because its type %s has no default btree operator class`, name, typ.Name()))
			}
			keys = append(keys, attribute)
			continue
		}
		recipe := bound[elementIndex]
		if len(s.Elements) > 1 {
			if _, ok := view.Catalog.UniqueDefaultIndexClass(403, recipe.Usage().Type); !ok {
				return sqlError("0A000", fmt.Sprintf("expression cannot be used in multivariate statistics because its type %s has no default btree operator class", recipe.Usage().Type.Name()))
			}
		}
		for _, prior := range expressions {
			if h.SameStatisticsExpression(prior, recipe) {
				return sqlError("42701", "duplicate expression in statistics definition")
			}
		}
		expressions = append(expressions, recipe)
		names = append(names, "expr")
	}
	if len(s.Elements) == 1 && len(expressions) == 1 && len(s.Kinds) != 0 {
		return sqlError("0A000", "when building statistics on a single expression, statistics kinds may not be specified")
	}
	kinds := ""
	if len(s.Kinds) == 0 {
		if len(s.Elements) > 1 {
			kinds = "dfm"
		}
	}
	for _, k := range s.Kinds {
		switch k.Text {
		case "ndistinct":
			kinds += "d"
		case "dependencies":
			kinds += "f"
		case "mcv":
			kinds += "m"
		default:
			return sqlError("42601", fmt.Sprintf(`unrecognized statistics kind "%s"`, k.Text))
		}
	}
	if len(s.Elements) < 2 && len(expressions) == 0 {
		return sqlError("42P17", "extended statistics require at least 2 columns")
	}
	for i, k := range keys {
		for _, prior := range keys[:i] {
			if k == prior {
				return sqlError("42701", "duplicate column name in statistics definition")
			}
		}
	}
	if len(expressions) != 0 {
		kinds += "e"
	}
	selection, err := catalog.NewStatisticsSelection(keys, len(expressions), kinds)
	if err != nil {
		return err
	}
	if object == "" {
		object = GeneratedRelationName(rel.Name(), strings.Join(names, "_"), "stat", func(name string) bool { key.Name = name; return h.Stage().LookupName(key) != (ids.ObjectAddress{}) })
	}
	oid, err := h.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := h.Revision()
	if err != nil {
		return err
	}
	h.AddDomainBase(rel)
	return CreateCommand(h.Stage(), "CREATE STATISTICS", &catalog.Statistics{ObjectAddress: ids.ObjectAddress{ClassID: ids.StatisticExtClassID, ObjectID: oid}, ObjectNamespace: ns.Address, ObjectName: object, ObjectRevision: revision, Owner: h.AccessView(auth).State.CurrentUser, Relation: ref, Selection: selection, Expressions: catalog.NewStatisticsExpressions(expressions)})
}

func StageDropStatistics(h StatisticsHost, auth Authority, s *parser.DropStatistics) error {
	for _, name := range s.Names {
		semantic := name
		var parts [2]parser.Identifier
		if len(name.Parts) == len(parts) {
			copy(parts[:], name.Parts)
			parts[0].Pos = parser.Position{}
			semantic.Parts = parts[:]
		}
		path, err := h.SearchNamespaces(auth, semantic, ids.StatisticExtClassID)
		if err != nil {
			if e, ok := err.(*sqlstate.Error); s.IfExists && ok && e.Code == "3F000" {
				Notice(h, "00000", e.Message+", skipping")
				continue
			}
			return err
		}
		var d *catalog.Statistics
		view := h.AccessView(auth)
		for _, ns := range path {
			a := view.Catalog.NameAddress(ids.NameKey{ClassID: ids.StatisticExtClassID, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text})
			if found, ok := view.Catalog.Statistics(a); ok {
				d = found
				break
			}
		}
		if d == nil {
			message := fmt.Sprintf(`statistics object "%s" does not exist`, catalog.ObjectNameText(name))
			if s.IfExists {
				Notice(h, "00000", message+", skipping")
				continue
			}
			return sqlError("42704", message)
		}
		err = Check(h, auth, access.Requirement{Object: ObjectRef(h, auth, d.Address()), OwnerOnly: true})
		if err != nil {
			if schemaErr := Check(h, auth, access.Requirement{Object: ObjectRef(h, auth, d.Namespace()), OwnerOnly: true}); schemaErr != nil {
				return err
			}
		}
		h.AddDomainBase(d)
		h.DropTarget(d.Address())
	}
	_ = h.RecordDependencyPath(auth)
	return nil
}
