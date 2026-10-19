package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

func validateExclusions(view catalog.Read, parent *catalog.Relation, names map[string]bool) error {
	for _, c := range parent.Constraints.Exclusion {
		index, ok := view.Relation(c.Index)
		if !ok || parent.RelationKind != catalog.TableRelation && parent.Partition == nil || c.OID == 0 || c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || index.SQLIndex == nil || !index.SQLIndex.IsExclusion() || index.Name() != c.Name || index.SQLIndex.Constraint.OID != c.OID || index.SQLIndex.Parent != parent.Address() || len(c.Operators) != len(index.SQLIndex.Keys) || c.InitiallyDeferred && !c.Deferrable {
			return fmt.Errorf("stuntgres: invalid exclusion constraint identity")
		}
		d := index.SQLIndex
		if d.Constraint.Name != c.Name || d.Constraint.Index != c.Index || len(d.Exclusion) != len(c.Operators) || len(c.Operators) == 0 || &d.Exclusion[0] != &c.Operators[0] {
			return fmt.Errorf("stuntgres: invalid exclusion operator ownership")
		}
		names[c.Name] = true
	}
	return nil
}

// Caller uses the installed catalog stage and index binder; no alternate type,
// expression, predicate or method provider is introduced by EXCLUDE.
func (e *Engine) stageExclusion(op *Operation, input sqltype.InputEnvironment, auth AuthorizationView, stage *CatalogStage, parent *catalog.Relation, raw *parser.ExclusionConstraint, recurse bool) error {
	s := raw.Index
	if len(s.Keys) == 0 || len(s.Keys) != len(raw.Operators) {
		return fmt.Errorf("stuntgres: invalid exclusion declaration")
	}
	if len(s.Keys)+len(s.Include) > 32 {
		return sqlError("54011", "cannot use more than 32 columns in an index")
	}
	if s.Method == "gin" || s.Method == "brin" {
		return sqlError("0A000", fmt.Sprintf(`access method "%s" does not support exclusion constraints`, s.Method))
	}
	methodCheck := s
	methodCheck.Predicate = nil // Constraint-local HASH predicate admission only.
	if err := validateIndexMethod(&methodCheck); err != nil {
		return err
	}
	auth.catalog = e.stageRead(stage)
	auth.Recapture(auth.catalog)
	predicate, err := e.bindIndexPredicate(op, input, auth, parent, s.Predicate)
	if err != nil {
		return err
	}
	definition, columns, attributes, lookup, err := e.bindIndexAttributes(op, input, auth, parent, &s)
	if err != nil {
		return err
	}
	definition.Predicate = predicate
	operators := make([]sqltype.ExclusionOperator, len(s.Keys))
	for i, name := range raw.Operators {
		path, err := e.lookupNamespaces(auth, name, operatorLookup)
		if err != nil {
			return indexSemanticError(err)
		}
		visible := false
		for j := range path.count() {
			if path.at(j) == (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) {
				visible = true
				break
			}
		}
		usage, err := indexKeyUsage(parent, definition.Keys[i])
		if err != nil {
			return err
		}
		operator := name.Parts[len(name.Parts)-1].Text
		if !visible {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: fmt.Sprintf("operator does not exist: %s %s %s", usage.Type.DiagnosticName(), objectNameText(name), usage.Type.DiagnosticName()), Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}
		}
		operators[i], err = sqltype.BindExclusionOperator(usage, definition.Keys[i].Class, operator)
		if err != nil {
			return err
		}
		if len(s.Keys[i].Options) != 0 {
			return sqlError("22023", fmt.Sprintf("operator class %s has no options", definition.Keys[i].Class.Name))
		}
	}
	if len(s.Options) != 0 {
		if err := validateIndexReloptions(s.Options, definition.Method, false); err != nil {
			return err
		}
		return indexCapability("index storage parameters are unsupported", s.WithPos)
	}
	name := raw.Name.Text
	used := constraintNames(auth.catalog, parent.Namespace(), parent)
	for key := range auth.catalog.Names() {
		if key.ClassID == ids.RelationClassID && key.Namespace == parent.Namespace() {
			used[key.Name] = true
		}
	}
	if name == "" {
		labels := make([]string, len(s.Keys))
		for i := range s.Keys {
			labels[i] = columns[i].Name
		}
		name = generatedConstraintName(parent.Name(), strings.Join(labels, "_"), "excl", used)
	} else {
		if _, altering := stage.Base.Find(parent.Address()); altering {
			if auth.catalog.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: name}) != (ids.ObjectAddress{}) {
				return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
			}
		}
		for _, c := range parent.Constraints.ForeignKeys {
			if c.Name == name {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.Name()))
			}
		}
		for _, c := range parent.Constraints.Unique {
			if c.Name == name {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.Name()))
			}
		}
		for _, c := range parent.Constraints.Exclusion {
			if c.Name == name {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.Name()))
			}
		}
		for _, c := range parent.Constraints.Check {
			if c.Name == name {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.Name()))
			}
		}
		for _, c := range parent.Constraints.NotNull {
			if c.Name == name {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.Name()))
			}
		}
		if used[name] {
			return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
	}
	constraintOID, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	indexOID, err := e.catalog.allocateOIDLocked()
	if err != nil {
		return err
	}
	revision, err := e.catalog.revisionLocked()
	if err != nil {
		return err
	}
	c := catalog.ExclusionConstraint{OID: constraintOID, Name: name, Index: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID}, IsLocal: true, Operators: operators, Deferrable: raw.Deferrable, InitiallyDeferred: raw.InitiallyDeferred}
	definition.Constraint = exclusionBacking(&c)
	definition.Exclusion = operators
	if err := partitionUniqueCoverage(parent, definition); err != nil {
		return err
	}
	index := &catalog.Relation{ObjectAddress: c.Index, ObjectNamespace: parent.Namespace(), ObjectName: name, ObjectRevision: revision, Owner: parent.Owner, RelationKind: catalog.IndexRelation, SQLIndex: definition, Columns: columns, Attributes: attributes, Index: lookup, ColumnMeta: catalog.ColumnMetadata{HighWater: int32(len(attributes))}}
	for i := range columns {
		index.Columns[i].Nullable = true
		index.Columns[i].NullabilityKnown = true
		index.Columns[i].OriginTable = uint32(indexOID)
		index.Columns[i].OriginAttribute = int16(i + 1)
	}
	parent.Constraints.Exclusion = append(parent.Constraints.Exclusion, c)
	if _, existing := stage.Base.Find(parent.Address()); existing {
		if err := stage.Replace(parent); err != nil {
			return err
		}
	}
	if err := stage.Create(index); err != nil {
		return err
	}
	if parent.Partition != nil {
		if !recurse {
			definition.Valid = len(parent.Partition.Children) == 0
			return nil
		}
		return e.stagePartitionIndexChildren(op, stage, parent, index, false)
	}
	return nil
}

func (e *Engine) stageCreateExclusions(op *Operation, input sqltype.InputEnvironment, auth AuthorizationView, stage *CatalogStage, parent *catalog.Relation, s *parser.CreateTable) error {
	for _, element := range s.Elements {
		if element.Exclusion != nil {
			if err := e.stageExclusion(op, input, auth, stage, parent, element.Exclusion, true); err != nil {
				return err
			}
		}
	}
	return nil
}

func exclusionDiagnostic(ctx context.Context, parent, index *catalog.Relation, key, existing []sqltype.Value, schema string, output sqltype.OutputEnvironment, allowed, building bool, catalog catalog.Read) error {
	message := fmt.Sprintf(`conflicting key value violates exclusion constraint "%s"`, index.Name())
	if building {
		message = fmt.Sprintf(`could not create exclusion constraint "%s"`, index.Name())
	}
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23P01", Message: message, Schema: schema, Table: parent.Name(), Constraint: index.Name(), Detail: "Key conflicts with existing key."}
	if building {
		d.Detail = "Key conflicts exist."
	}
	if !allowed {
		return d
	}
	labels := make([]string, len(key))
	values := make([]string, len(key))
	prior := make([]string, len(key))
	for i, value := range key {
		attribute, node, err := indexCatalogKey(parent, index.SQLIndex.Keys[i])
		if err != nil {
			return err
		}
		if node != nil {
			// Index-key syntax groups a top-level operator without imposing the
			// fully parenthesized catalog renderer on casts inside functions.
			labels[i], err = (pgRenderer{view: catalog, relation: parent, pretty: true, output: output}).sql(ctx, node, pgRenderParent{precedence: 6})
		} else {
			labels[i], err = deparseAttribute(parent, attribute)
		}
		if err != nil {
			return err
		}
		values[i], err = value.Type().OutputWithEnvironment(ctx, value, output)
		if err != nil {
			return err
		}
		prior[i], err = existing[i].Type().OutputWithEnvironment(ctx, existing[i], output)
		if err != nil {
			return err
		}
	}
	middle := " conflicts with existing key "
	if building {
		middle = " conflicts with key "
	}
	label := "(" + strings.Join(labels, ", ") + ")"
	d.Detail = "Key " + label + "=(" + strings.Join(values, ", ") + ")" + middle + label + "=(" + strings.Join(prior, ", ") + ")."
	return d
}
