package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

func validateUniques(view catalog.Read, parent *catalog.Relation, names map[string]bool) error {
	primary := false
	var physicalSupport *catalog.PhysicalPrimarySupport
	for _, c := range parent.Constraints.Unique {
		if (parent.RelationKind != catalog.TableRelation && parent.RelationKind != catalog.PartitionedRelation) || c.OID == 0 || c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || len(c.Attributes) == 0 || len(c.Attributes) > 32 {
			return fmt.Errorf("stuntgres: invalid unique constraint identity")
		}
		names[c.Name] = true
		if c.Primary {
			if primary || c.NullsNotDistinct {
				return fmt.Errorf("stuntgres: invalid primary constraint")
			}
			primary = true
		}
		index, ok := view.Relation(c.Index)
		if !ok || index.SQLIndex == nil || index.Name() != c.Name || index.SQLIndex.Parent != parent.Address() || !index.SQLIndex.Unique || index.SQLIndex.Constraint == nil || index.SQLIndex.Constraint.Kind != catalog.UniqueBackingKind || index.SQLIndex.Constraint.OID != c.OID || index.SQLIndex.Constraint.Index != c.Index || index.SQLIndex.Constraint.Name != c.Name || index.SQLIndex.NullsNotDistinct != c.NullsNotDistinct || len(index.SQLIndex.Keys) != len(c.Attributes) {
			return fmt.Errorf("stuntgres: inconsistent unique backing index")
		}
		if index.SQLIndex.Constraint.Primary != c.Primary {
			return fmt.Errorf("stuntgres: inconsistent primary backing index")
		}
		used := make(map[int32]bool, len(c.Attributes))
		for i, a := range c.Attributes {
			attribute, expression, err := indexCatalogKey(parent, index.SQLIndex.Keys[i])
			if err != nil || expression != nil || used[a] || !parent.HasAttribute(a) || attribute != a || index.SQLIndex.Keys[i].Desc || index.SQLIndex.Keys[i].NullsFirst {
				return fmt.Errorf("stuntgres: invalid unique key")
			}
			used[a] = true
			if c.Primary {
				slot := slices.Index(parent.Attributes, a)
				if parent.Columns[slot].Nullable || !parent.Columns[slot].NullabilityKnown {
					return fmt.Errorf("stuntgres: nullable primary key")
				}
				found := false
				for _, n := range parent.Constraints.NotNull {
					if n.Attribute == a {
						found = true
						break
					}
				}
				if !found {
					proof, exists := parent.Constraints.PhysicalNotNull[a]
					if !exists || !physicalNotNullOriginValid(view, parent, a, proof) || proof.Support == nil {
						return fmt.Errorf("stuntgres: missing primary not-null identity")
					}
					if physicalSupport != proof.Support {
						if !physicalPrimarySupportValid(view, parent, &c, proof.Support) {
							return fmt.Errorf("stuntgres: invalid physical primary not-null support")
						}
						physicalSupport = proof.Support
					}
				}
			}
		}
	}
	return nil
}

func (e *Engine) stageCreateUniques(op *Operation, stage *CatalogStage, d *catalog.Relation, declarations []createUniqueConstraint) error {
	return e.stageUniqueDefinitions(op, stage, d, declarations, false)
}

// CREATE and ALTER share index/provider/ownership construction. ALTER appends
// only new groups and replaces the existing parent instead of creating it.
func (e *Engine) stageUniqueDefinitions(op *Operation, stage *CatalogStage, d *catalog.Relation, declarations []createUniqueConstraint, altering bool) error {
	for _, declaration := range declarations {
		for _, attribute := range declaration.Attributes {
			if definition, ok := d.ColumnMeta.Generations[attribute]; ok && definition.Kind == catalog.GenerationVirtual {
				message := "unique constraints on virtual generated columns are not supported"
				if declaration.Primary {
					message = "primary keys on virtual generated columns are not supported"
				}
				return sqlError("0A000", message)
			}
		}
	}
	start := len(d.Constraints.Unique)
	constraints := make([]catalog.UniqueConstraint, start+len(declarations))
	copy(constraints, d.Constraints.Unique)
	d.Constraints.Unique = constraints
	for i := range declarations {
		d.Constraints.Unique[start+i] = declarations[i].UniqueConstraint
		d.Constraints.Unique[start+i].IsLocal = true
		if source := declarations[i].Source; source != nil {
			stage.captureLikeSource(source)
		}
	}
	local := make(map[string]bool)
	for _, c := range d.Constraints.NotNull {
		local[c.Name] = true
	}
	for _, c := range d.Constraints.Check {
		local[c.Name] = true
	}
	for _, c := range d.Constraints.Unique[:start] {
		local[c.Name] = true
	}
	for _, c := range d.Constraints.Exclusion {
		local[c.Name] = true
	}
	for _, c := range d.Constraints.ForeignKeys {
		local[c.Name] = true
	}
	view := e.stageRead(stage)
	used := constraintNames(view, d.Namespace(), nil)
	for name := range local {
		used[name] = true
	}
	for key := range view.Names() {
		if key.ClassID == ids.RelationClassID && key.Namespace == d.Namespace() {
			used[key.Name] = true
		}
	}
	used[d.Name()] = true
	indices := make([]*catalog.Relation, 0, len(declarations))
	for i := start; i < len(d.Constraints.Unique); i++ {
		c := &d.Constraints.Unique[i]
		if c.Name == d.Name() {
			return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
		}
		if c.Name == "" {
			if c.Primary {
				c.Name = generatedConstraintName(d.Name(), "", "pkey", used)
			} else {
				labels := make([]string, len(c.Attributes))
				for j, a := range c.Attributes {
					labels[j] = d.Columns[slices.Index(d.Attributes, a)].Name
				}
				c.Name = generatedConstraintName(d.Name(), strings.Join(labels, "_"), "key", used)
			}
		}
		// An existing backing relation wins over a same-named local NN/CHECK
		// for PK. Ordinary UNIQUE retains its existing collision precedence.
		if c.Primary || altering {
			key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: c.Name}
			if stage.LookupName(key) != (ids.ObjectAddress{}) {
				return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
			}
		}
		if local[c.Name] {
			return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, c.Name, d.Name()))
		}
		// Two distinct UNIQUE definitions with the same name collide as relations.
		key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: c.Name}
		if stage.LookupName(key) != (ids.ObjectAddress{}) {
			return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
		}
		for _, index := range indices {
			if index.Name() == c.Name {
				return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
			}
		}
		used[c.Name] = true
		var err error
		c.OID, err = e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		id, err := e.catalog.allocateOIDLocked()
		if err != nil {
			return err
		}
		c.Index = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
		revision, err := e.catalog.revisionLocked()
		if err != nil {
			return err
		}
		definition := &catalog.IndexDefinition{Method: catalog.BtreeMethod, Parent: d.Address(), Unique: true, NullsNotDistinct: c.NullsNotDistinct, Constraint: uniqueBacking(c), Ready: true, Valid: true, Live: true}
		index := &catalog.Relation{ObjectAddress: c.Index, ObjectNamespace: d.Namespace(), ObjectName: c.Name, ObjectRevision: revision, Owner: d.Owner, RelationKind: catalog.IndexRelation, SQLIndex: definition}
		if source := declarations[i-start].Source; source != nil {
			// LIKE clones the actual key providers and presentation, not a
			// freshly defaulted approximation or any source storage/root.
			*definition = *source.SQLIndex
			definition.Parent, definition.Constraint = d.Address(), uniqueBacking(c)
			definition.PartitionParent = ids.ObjectAddress{}
			definition.Ready, definition.Valid, definition.Live = true, true, true
			definition.Keys = slices.Clone(source.SQLIndex.Keys)
			for j := range definition.Keys {
				key := &definition.Keys[j]
				if key.Derived == nil {
					key.Attribute = c.Attributes[j]
				} else {
					node, err := remapLikeExpression(declarations[i-start].Like, key.Derived.Definition, d, stage.Base)
					if err != nil {
						return err
					}
					execution, err := remapLikeExpression(declarations[i-start].Like, key.Derived.Execution, d, stage.Base)
					if err != nil {
						return err
					}
					derived := &catalog.DerivedIndexKey{Definition: node, Execution: execution, Attributes: d.Attributes}
					if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: e.catalog.owner(), roles: e.roles.read(e.catalog.owner(), stage.Base, e.operationRolesLocked(op))}, owner: d, dependencies: &derived.Dependencies}).sql(op.Context(), stage.Base, node); err != nil {
						return err
					}
					key.Derived = derived
				}
			}
			definition.Include = make([]int32, len(source.SQLIndex.Include))
			for j, attribute := range source.SQLIndex.Include {
				definition.Include[j], err = declarations[i-start].Like.Attribute(d, attribute)
				if err != nil {
					return err
				}
			}
			index.Columns = slices.Clone(source.Columns)
			index.Attributes = slices.Clone(source.Attributes)
			index.ColumnMeta.HighWater = source.ColumnMeta.HighWater
			if declarations[i-start].CopyComments {
				index.Comment = source.Comment
			}
		} else {
			definition.Keys = make([]catalog.IndexKey, len(c.Attributes))
			index.Columns = make([]catalog.Column, len(c.Attributes))
			index.Attributes = make([]int32, len(c.Attributes))
			index.ColumnMeta.HighWater = int32(len(c.Attributes))
			for j, a := range c.Attributes {
				column := d.Columns[slices.Index(d.Attributes, a)]
				class, ok := sqltype.DefaultIndexClass(column.Usage.Type)
				if !ok {
					return indexDefaultClassError(column.Usage.Type.DiagnosticName(), catalog.BtreeMethod)
				}
				definition.Keys[j] = catalog.IndexKey{Attribute: a, Class: class}
				column.Nullable, column.NullabilityKnown = true, true
				index.Columns[j] = column
				index.Attributes[j] = int32(j + 1)
			}
		}
		index.Index = make(map[string]int, len(index.Columns))
		for j := range index.Columns {
			column := &index.Columns[j]
			column.OriginTable = uint32(id)
			column.OriginAttribute = int16(j + 1)
			index.Index[column.Name] = j
		}
		indices = append(indices, index)
	}
	var err error
	if altering {
		err = stage.Replace(d)
	} else {
		err = stage.Create(d)
	}
	if err != nil {
		return err
	}
	for _, index := range indices {
		if err := stage.Create(index); err != nil {
			return err
		}
	}
	return nil
}

func uniqueHasNull(key []sqltype.Value) bool {
	for _, v := range key {
		if v.IsNull() {
			return true
		}
	}
	return false
}

// DDL ownership is independent of ordinary key SELECT disclosure rights.
func uniqueKeyDetailAllowed(auth AuthorizationView, ref ids.ObjectRef, keys []catalog.IndexKey) (bool, error) {
	// RLS applicability is captured metadata/role authority, not a policy-row
	// evaluation. Table SELECT alone cannot reveal a hidden physical key.
	descriptor, ok := auth.Catalog.Descriptor(ref.Address)
	parent, relation := descriptor.(*catalog.Relation)
	if !ok || !relation {
		return false, fmt.Errorf("stuntgres: unique disclosure parent is missing")
	}
	decision, err := access.RowSecurityDecision(auth.Roles, parent, auth.State.CurrentUser, catalog.PolicySelect, false)
	if err != nil || decision != access.RLSUnrestricted {
		return false, err
	}
	rights, err := auth.Effective(ref, false)
	if err != nil {
		return false, err
	}
	if rights&authorization.SelectPrivilege != 0 {
		return true, nil
	}
	for _, key := range keys {
		attribute, expression, err := indexCatalogKey(parent, key)
		if err != nil {
			return false, err
		}
		// Column privileges do not authorize disclosure of an actual
		// expression, even when every dependency is readable.
		if expression != nil {
			return false, nil
		}
		column := ref
		column.Address.SubID = attribute
		rights, err := auth.Effective(column, false)
		if err != nil {
			return false, err
		}
		if rights&authorization.SelectPrivilege == 0 {
			return false, nil
		}
	}
	return true, nil
}

// A domain's index attribute uses the physical oid_ops descriptor for output.
// Produced keys retain their logical alias type for identity remapping; only
// diagnostic formatting normalizes that datum, without checking it again.
func uniqueKeyOutput(ctx context.Context, value sqltype.Value, class sqltype.IndexClass, catalog catalog.Read, output sqltype.OutputEnvironment) (string, error) {
	if value.Type().IsDomain() && class.Name == "oid_ops" {
		oid, ok := catalog.LookupType(sqltype.Oid)
		if !ok {
			return "", fmt.Errorf("stuntgres: index OID output descriptor is missing")
		}
		var err error
		value, err = class.OIDKeyValue(value, oid)
		if err != nil {
			return "", err
		}
	}
	return value.Type().OutputWithEnvironment(ctx, value, output)
}

// Formatting uses typed provider output, never SQL literals or Go formatting.
func uniqueDiagnostic(ctx context.Context, parent, index *catalog.Relation, key []sqltype.Value, schema string, output sqltype.OutputEnvironment, detailAllowed, building bool, catalog catalog.Read) error {
	message := fmt.Sprintf(`duplicate key value violates unique constraint "%s"`, index.Name())
	if building {
		message = fmt.Sprintf(`could not create unique index "%s"`, index.Name())
	}
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: message, Schema: schema, Table: parent.Name(), Constraint: index.Name()}
	if detailAllowed {
		labels := make([]string, len(key))
		values := make([]string, len(key))
		for i, value := range key {
			attribute, node, err := indexCatalogKey(parent, index.SQLIndex.Keys[i])
			if err != nil {
				return err
			}
			if node != nil {
				labels[i], err = (pgRenderer{view: catalog, relation: parent, output: output}).sql(ctx, node, pgRenderParent{})
			} else {
				labels[i], err = deparseAttribute(parent, attribute)
			}
			if err != nil {
				return err
			}
			if value.IsNull() {
				values[i] = "null"
			} else {
				text, err := uniqueKeyOutput(ctx, value, index.SQLIndex.Keys[i].Class, catalog, output)
				if err != nil {
					return err
				}
				values[i] = text
			}
		}
		suffix := " already exists."
		if building {
			suffix = " is duplicated."
		}
		d.Detail = "Key (" + strings.Join(labels, ", ") + ")=(" + strings.Join(values, ", ") + ")" + suffix
	} else if building {
		d.Detail = "Duplicate keys exist."
	}
	return d
}

func validateUniqueRoot(ctx context.Context, parent, index *catalog.Relation, root *storage.IndexRoot, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool) error {
	d := index.SQLIndex
	if d.IsExclusion() {
		return validateExclusionRoot(ctx, parent, index, root, schema, output, auth, building)
	}
	if !d.Unique {
		return nil
	}
	ordering, err := bindIndexOrdering(parent, d)
	if err != nil {
		return err
	}
	return validateUniqueEntries(ctx, parent, index, root.Entries, schema, output, auth, building, ordering)
}

func validateUniqueEntries(ctx context.Context, parent, index *catalog.Relation, entries []storage.IndexEntry, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool, ordering indexOrdering) error {
	if index.SQLIndex.IsExclusion() {
		return validateExclusionEntries(ctx, parent, index, entries, schema, output, auth, building)
	}
	for i := 1; i < len(entries); i++ {
		if err := validateUniquePair(ctx, parent, index, entries[i-1], entries[i], schema, output, auth, building, ordering); err != nil {
			return err
		}
	}
	return nil
}

func validateUniquePair(ctx context.Context, parent, index *catalog.Relation, previous, current storage.IndexEntry, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool, ordering indexOrdering) error {
	d := index.SQLIndex
	if d.IsExclusion() {
		return validateExclusionPair(ctx, parent, index, previous, current, schema, output, auth, building)
	}
	if !d.Unique || !d.NullsNotDistinct && uniqueHasNull(current.Keys) {
		return nil
	}
	cmp, err := ordering.compareKeys(ctx, previous, current)
	if err != nil {
		return err
	}
	if cmp == 0 {
		allowed := auth == nil || auth.engine == nil // Trusted publication has no SQL principal.
		if auth != nil && auth.engine != nil {
			ref := auth.Catalog.Owner().Domain.Ref(auth.Catalog.Epoch(), parent.Address())
			allowed, err = uniqueKeyDetailAllowed(*auth, ref, d.Keys)
			if err != nil {
				return err
			}
		}
		key := current.Keys
		if building {
			// Report an actual earlier comparator tuple for builds, not a
			// normalized equality value. Validation reports the candidate.
			key = previous.Keys
		}
		var catalog catalog.Read
		if auth != nil {
			catalog = auth.Catalog
		}
		return uniqueDiagnostic(ctx, parent, index, key, schema, output, allowed, building, catalog)
	}
	return nil
}

func (r pgRenderer) uniqueConstraintSQL(parent *catalog.Relation, c *catalog.UniqueConstraint) (string, error) {
	index, ok := r.view.Relation(c.Index)
	if !ok || index.SQLIndex == nil {
		return "", fmt.Errorf("stuntgres: missing unique deparse index")
	}
	labels := make([]string, len(c.Attributes)+len(index.SQLIndex.Include))
	for i, attribute := range c.Attributes {
		label, err := r.attribute(parent, attribute)
		if err != nil {
			return "", err
		}
		labels[i] = label
	}
	prefix := "UNIQUE"
	if c.Primary {
		prefix = "PRIMARY KEY"
	} else if c.NullsNotDistinct {
		prefix += " NULLS NOT DISTINCT"
	}
	text := prefix + " (" + strings.Join(labels[:len(c.Attributes)], ", ") + ")"
	if len(index.SQLIndex.Include) != 0 {
		for i, attribute := range index.SQLIndex.Include {
			label, err := r.attribute(parent, attribute)
			if err != nil {
				return "", err
			}
			labels[len(c.Attributes)+i] = label
		}
		text += " INCLUDE (" + strings.Join(labels[len(c.Attributes):], ", ") + ")"
	}
	return text, nil
}
