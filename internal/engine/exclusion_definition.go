package engine

import (
	"context"
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

type exclusionConstraint struct {
	OID                           OID
	Name                          string
	Index                         ObjectAddress
	Operators                     []sqltype.ExclusionOperator
	Deferrable, InitiallyDeferred bool
}

func validateExclusions(view CatalogRead, parent *relationDescriptor, names map[string]bool) error {
	for _, c := range parent.exclusionConstraints {
		index, ok := view.state.descriptors[c.Index].(*relationDescriptor)
		if !ok || parent.relationKind != tableRelation || c.OID == 0 || c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || index.sqlIndex == nil || !index.sqlIndex.isExclusion() || index.name != c.Name || index.sqlIndex.constraint.OID != c.OID || index.sqlIndex.parent != parent.address || len(c.Operators) != len(index.sqlIndex.keys) || c.InitiallyDeferred && !c.Deferrable {
			return fmt.Errorf("stuntgres: invalid exclusion constraint identity")
		}
		d := index.sqlIndex
		if d.constraint.Name != c.Name || d.constraint.Index != c.Index || len(d.exclusion) != len(c.Operators) || len(c.Operators) == 0 || &d.exclusion[0] != &c.Operators[0] {
			return fmt.Errorf("stuntgres: invalid exclusion operator ownership")
		}
		names[c.Name] = true
	}
	return nil
}

// Caller uses the installed catalog stage and index binder; no alternate type,
// expression, predicate or method provider is introduced by EXCLUDE.
func (e *Engine) stageExclusion(op *Operation, input sqltype.InputEnvironment, auth AuthorizationView, stage *CatalogStage, parent *relationDescriptor, raw *parser.ExclusionConstraint) error {
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
	auth.recaptureCatalog(auth.catalog)
	predicate, err := e.bindIndexPredicate(op, input, auth, parent, s.Predicate)
	if err != nil {
		return err
	}
	definition, columns, attributes, lookup, err := e.bindIndexAttributes(op, input, auth, parent, &s)
	if err != nil {
		return err
	}
	definition.predicate = predicate
	operators := make([]sqltype.ExclusionOperator, len(s.Keys))
	for i, name := range raw.Operators {
		path, err := e.lookupNamespaces(auth, name, operatorLookup)
		if err != nil {
			return indexSemanticError(err)
		}
		visible := false
		for j := range path.count() {
			if path.at(j) == (ObjectAddress{ClassID: NamespaceClassID, ObjectID: PGCatalogNamespaceOID}) {
				visible = true
				break
			}
		}
		usage, err := indexKeyUsage(parent, definition.keys[i])
		if err != nil {
			return err
		}
		operator := name.Parts[len(name.Parts)-1].Text
		if !visible {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: fmt.Sprintf("operator does not exist: %s %s %s", usage.Type.DiagnosticName(), objectNameText(name), usage.Type.DiagnosticName()), Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}
		}
		operators[i], err = sqltype.BindExclusionOperator(usage, definition.keys[i].class, operator)
		if err != nil {
			return err
		}
		if len(s.Keys[i].Options) != 0 {
			return sqlError("22023", fmt.Sprintf("operator class %s has no options", definition.keys[i].class.Name))
		}
	}
	if len(s.Options) != 0 {
		if err := validateIndexReloptions(s.Options, definition.method, false); err != nil {
			return err
		}
		return indexCapability("index storage parameters are unsupported", s.WithPos)
	}
	name := raw.Name.Text
	used := constraintNames(auth.catalog, parent.namespace, parent)
	for key := range auth.catalog.state.names {
		if key.ClassID == RelationClassID && key.Namespace == parent.namespace {
			used[key.Name] = true
		}
	}
	if name == "" {
		labels := make([]string, len(s.Keys))
		for i := range s.Keys {
			labels[i] = columns[i].Name
		}
		name = generatedConstraintName(parent.name, strings.Join(labels, "_"), "excl", used)
	} else {
		if _, altering := stage.base.state.descriptors[parent.address]; altering {
			if auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: parent.namespace, Name: name}] != (ObjectAddress{}) {
				return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
			}
		}
		for _, c := range parent.foreignKeys {
			if c.Name == name {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.name))
			}
		}
		for _, c := range parent.uniqueConstraints {
			if c.Name == name {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.name))
			}
		}
		for _, c := range parent.exclusionConstraints {
			if c.Name == name {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.name))
			}
		}
		for _, c := range parent.checkConstraints {
			if c.Name == name {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.name))
			}
		}
		for _, c := range parent.notNullConstraints {
			if c.Name == name {
				return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, name, parent.name))
			}
		}
		if used[name] {
			return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, name))
		}
	}
	constraintOID, err := e.allocateOIDLocked()
	if err != nil {
		return err
	}
	indexOID, err := e.allocateOIDLocked()
	if err != nil {
		return err
	}
	revision, err := e.revisionLocked()
	if err != nil {
		return err
	}
	c := exclusionConstraint{OID: constraintOID, Name: name, Index: ObjectAddress{ClassID: RelationClassID, ObjectID: indexOID}, Operators: operators, Deferrable: raw.Deferrable, InitiallyDeferred: raw.InitiallyDeferred}
	definition.constraint = exclusionBacking(&c)
	definition.exclusion = operators
	index := &relationDescriptor{address: c.Index, namespace: parent.namespace, name: name, revision: revision, owner: parent.owner, relationKind: indexRelation, sqlIndex: definition, columns: columns, attributes: attributes, index: lookup, attributeHighWater: int32(len(attributes))}
	for i := range columns {
		index.columns[i].Nullable = true
		index.columns[i].NullabilityKnown = true
		index.columns[i].OriginTable = uint32(indexOID)
		index.columns[i].OriginAttribute = int16(i + 1)
	}
	parent.exclusionConstraints = append(parent.exclusionConstraints, c)
	if _, existing := stage.base.state.descriptors[parent.address]; existing {
		if err := stage.Replace(parent); err != nil {
			return err
		}
	}
	return stage.Create(index)
}

func (e *Engine) stageCreateExclusions(op *Operation, input sqltype.InputEnvironment, auth AuthorizationView, stage *CatalogStage, parent *relationDescriptor, s *parser.CreateTable) error {
	for _, element := range s.Elements {
		if element.Exclusion != nil {
			if err := e.stageExclusion(op, input, auth, stage, parent, element.Exclusion); err != nil {
				return err
			}
		}
	}
	return nil
}

func exclusionDiagnostic(ctx context.Context, parent, index *relationDescriptor, key, existing []sqltype.Value, schema string, output sqltype.OutputEnvironment, allowed, building bool, catalog CatalogRead) error {
	message := fmt.Sprintf(`conflicting key value violates exclusion constraint "%s"`, index.name)
	if building {
		message = fmt.Sprintf(`could not create exclusion constraint "%s"`, index.name)
	}
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23P01", Message: message, Schema: schema, Table: parent.name, Constraint: index.name, Detail: "Key conflicts with existing key."}
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
		attribute, node, err := indexCatalogKey(parent, index.sqlIndex.keys[i])
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
