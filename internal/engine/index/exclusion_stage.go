package index

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// StageExclusion stages one EXCLUDE constraint on parent with its backing
// index; recurse also stages the partitions' child indexes. It binds through
// the stage's catalog and the host's index binder; EXCLUDE introduces no
// alternate type, expression, predicate or method provider.
func StageExclusion(h Host, auth ddl.Authority, input sqltype.InputEnvironment, parent *catalog.Relation, raw *parser.ExclusionConstraint, recurse bool) error {
	s := raw.Index
	if len(s.Keys) == 0 || len(s.Keys) != len(raw.Operators) {
		return fmt.Errorf("stuntgres: invalid exclusion declaration")
	}
	if len(s.Keys)+len(s.Include) > 32 {
		return sqlError("54011", "cannot use more than 32 columns in an index")
	}
	if _, exists := h.StageRead().LookupAccessMethod(s.Method); !exists && s.Method == "gin" {
		return sqlError("0A000", fmt.Sprintf(`access method "%s" does not support exclusion constraints`, s.Method))
	}
	methodCheck := s
	methodCheck.Predicate = nil // Constraint-local HASH predicate admission only.
	method, err := ResolveIndexMethod(h.StageRead(), &methodCheck)
	if err != nil {
		return err
	}
	physical, _ := method.IndexImplementation()
	if exclude, known := MethodProperty(physical, "can_exclude"); !known || !exclude {
		return sqlError("0A000", fmt.Sprintf(`access method "%s" does not support exclusion constraints`, method.Name()))
	}
	auth = h.Recapture(auth, h.StageRead())
	view := h.AccessView(auth).Catalog
	predicate, err := h.BindIndexPredicate(auth, parent, s.Predicate, input)
	if err != nil {
		return err
	}
	definition, columns, attributes, lookup, err := bindAttributes(h, auth, input, parent, &s, method)
	if err != nil {
		return err
	}
	definition.Predicate = predicate
	operators := make([]sqltype.ExclusionOperator, len(s.Keys))
	for i, name := range raw.Operators {
		path, err := h.SearchNamespaces(auth, name, ids.OperatorClassID)
		if err != nil {
			return SemanticError(err)
		}
		visible := false
		for _, namespace := range path {
			if namespace == (ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}) {
				visible = true
				break
			}
		}
		usage, err := KeyUsage(parent, definition.Keys[i])
		if err != nil {
			return err
		}
		operator := name.Parts[len(name.Parts)-1].Text
		if !visible {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: fmt.Sprintf("operator does not exist: %s %s %s", usage.Type.DiagnosticName(), catalog.ObjectNameText(name), usage.Type.DiagnosticName()), Hint: "No operator matches the given name and argument types. You might need to add explicit type casts."}
		}
		operators[i], err = routine.BindExclusionOperator(h.StageRead(), usage, definition.Keys[i].Class, operator)
		if err != nil {
			return err
		}
		definition.Keys[i].ClassOptions, err = BindClassOptions(view, definition.Keys[i].Class, s.Keys[i].Options)
		if err != nil {
			return err
		}
	}
	var options catalog.RelationOptions
	if len(s.Options) != 0 {
		if err := ValidateIndexReloptions(s.Options, definition.Method, false); err != nil {
			return err
		}
		if definition.Method != catalog.GistMethod && definition.Method != catalog.SpgistMethod {
			return Capability("index storage parameters are unsupported", s.WithPos)
		}
		options, err = TransformIndexOptions(catalog.RelationOptions{}, &parser.AlterIndex{Action: "set", Options: s.Options}, definition.Method)
		if err != nil {
			return err
		}
	}
	name := raw.Name.Text
	used := catalog.ConstraintNames(view, parent.Namespace(), parent)
	for key := range view.Names() {
		if key.ClassID == ids.RelationClassID && key.Namespace == parent.Namespace() {
			used[key.Name] = true
		}
	}
	if name == "" {
		labels := make([]string, len(s.Keys))
		for i := range s.Keys {
			labels[i] = columns[i].Name
		}
		name = catalog.GeneratedConstraintName(parent.Name(), strings.Join(labels, "_"), "excl", used)
	} else {
		if _, altering := h.StageBase().Find(parent.Address()); altering {
			if view.NameAddress(ids.NameKey{ClassID: ids.RelationClassID, Namespace: parent.Namespace(), Name: name}) != (ids.ObjectAddress{}) {
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
	constraintOID, err := h.AllocateOID()
	if err != nil {
		return err
	}
	indexOID, err := h.AllocateOID()
	if err != nil {
		return err
	}
	revision, err := h.Revision()
	if err != nil {
		return err
	}
	c := catalog.ExclusionConstraint{OID: constraintOID, Name: name, Index: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: indexOID}, IsLocal: true, Operators: operators, Deferrable: raw.Deferrable, InitiallyDeferred: raw.InitiallyDeferred}
	definition.Constraint = ExclusionBacking(&c)
	definition.Exclusion = operators
	if err := PartitionUniqueCoverage(h.StageRead(), parent, definition); err != nil {
		return DefaultClassDiagnostic(h, auth, err)
	}
	backing := &catalog.Relation{ObjectAddress: c.Index, ObjectNamespace: parent.Namespace(), ObjectName: name, ObjectRevision: revision, Owner: parent.Owner, RelationKind: catalog.IndexRelation, SQLIndex: definition, Columns: columns, Attributes: attributes, Index: lookup, ColumnMeta: catalog.ColumnMetadata{HighWater: int32(len(attributes))}, StorageOptions: options}
	for i := range columns {
		backing.Columns[i].Nullable = true
		backing.Columns[i].NullabilityKnown = true
		backing.Columns[i].OriginTable = uint32(indexOID)
		backing.Columns[i].OriginAttribute = int16(i + 1)
	}
	parent.Constraints.Exclusion = append(parent.Constraints.Exclusion, c)
	if _, existing := h.StageBase().Find(parent.Address()); existing {
		if err := h.Stage().Replace(parent); err != nil {
			return err
		}
	}
	if err := h.Stage().Create(backing); err != nil {
		return err
	}
	if parent.Partition != nil {
		if !recurse {
			definition.Valid = len(parent.Partition.Children) == 0
			return nil
		}
		return DefaultClassDiagnostic(h, auth, StagePartitionIndexChildren(h, parent, backing, false))
	}
	return nil
}

// StageCreateExclusions stages the EXCLUDE constraints of CREATE TABLE s on
// the new table parent.
func StageCreateExclusions(h Host, auth ddl.Authority, input sqltype.InputEnvironment, parent *catalog.Relation, s *parser.CreateTable) error {
	for _, element := range s.Elements {
		if element.Exclusion != nil {
			if err := StageExclusion(h, auth, input, parent, element.Exclusion, true); err != nil {
				return err
			}
		}
	}
	return nil
}
