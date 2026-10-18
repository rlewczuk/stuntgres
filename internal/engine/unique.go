package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

// Constraint and backing relation are separate identities published together.
type uniqueConstraint struct {
	OID              OID
	Name             string
	Attributes       []int32
	Primary          bool
	NullsNotDistinct bool
	Index            ObjectAddress
	Parent           OID
	IsLocal          bool
}

func validateUniques(view CatalogRead, parent *relationDescriptor, names map[string]bool) error {
	primary := false
	var physicalSupport *physicalPrimarySupport
	for _, c := range parent.uniqueConstraints {
		if (parent.relationKind != tableRelation && parent.relationKind != partitionedRelation) || c.OID == 0 || c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || len(c.Attributes) == 0 || len(c.Attributes) > 32 {
			return fmt.Errorf("stuntgres: invalid unique constraint identity")
		}
		names[c.Name] = true
		if c.Primary {
			if primary || c.NullsNotDistinct {
				return fmt.Errorf("stuntgres: invalid primary constraint")
			}
			primary = true
		}
		index, ok := view.state.descriptors[c.Index].(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.name != c.Name || index.sqlIndex.parent != parent.address || !index.sqlIndex.unique || index.sqlIndex.constraint == nil || index.sqlIndex.constraint.kind != uniqueBackingKind || index.sqlIndex.constraint.OID != c.OID || index.sqlIndex.constraint.Index != c.Index || index.sqlIndex.constraint.Name != c.Name || index.sqlIndex.nullsNotDistinct != c.NullsNotDistinct || len(index.sqlIndex.keys) != len(c.Attributes) {
			return fmt.Errorf("stuntgres: inconsistent unique backing index")
		}
		if index.sqlIndex.constraint.Primary != c.Primary {
			return fmt.Errorf("stuntgres: inconsistent primary backing index")
		}
		used := make(map[int32]bool, len(c.Attributes))
		for i, a := range c.Attributes {
			attribute, expression, err := indexCatalogKey(parent, index.sqlIndex.keys[i])
			if err != nil || expression != nil || used[a] || !parent.hasAttribute(a) || attribute != a || index.sqlIndex.keys[i].desc || index.sqlIndex.keys[i].nullsFirst {
				return fmt.Errorf("stuntgres: invalid unique key")
			}
			used[a] = true
			if c.Primary {
				slot := slices.Index(parent.attributes, a)
				if parent.columns[slot].Nullable || !parent.columns[slot].NullabilityKnown {
					return fmt.Errorf("stuntgres: nullable primary key")
				}
				found := false
				for _, n := range parent.notNullConstraints {
					if n.Attribute == a {
						found = true
						break
					}
				}
				if !found {
					proof, exists := parent.physicalNotNull[a]
					if !exists || !physicalNotNullOriginValid(view, parent, a, proof) || proof.support == nil {
						return fmt.Errorf("stuntgres: missing primary not-null identity")
					}
					if physicalSupport != proof.support {
						if !physicalPrimarySupportValid(view, parent, &c, proof.support) {
							return fmt.Errorf("stuntgres: invalid physical primary not-null support")
						}
						physicalSupport = proof.support
					}
				}
			}
		}
	}
	return nil
}

func (e *Engine) stageCreateUniques(op *Operation, stage *CatalogStage, d *relationDescriptor, declarations []createUniqueConstraint) error {
	return e.stageUniqueDefinitions(op, stage, d, declarations, false)
}

// CREATE and ALTER share index/provider/ownership construction. ALTER appends
// only new groups and replaces the existing parent instead of creating it.
func (e *Engine) stageUniqueDefinitions(op *Operation, stage *CatalogStage, d *relationDescriptor, declarations []createUniqueConstraint, altering bool) error {
	for _, declaration := range declarations {
		for _, attribute := range declaration.Attributes {
			if definition, ok := d.generations[attribute]; ok && definition.kind == generationVirtual {
				message := "unique constraints on virtual generated columns are not supported"
				if declaration.Primary {
					message = "primary keys on virtual generated columns are not supported"
				}
				return sqlError("0A000", message)
			}
		}
	}
	start := len(d.uniqueConstraints)
	constraints := make([]uniqueConstraint, start+len(declarations))
	copy(constraints, d.uniqueConstraints)
	d.uniqueConstraints = constraints
	for i := range declarations {
		d.uniqueConstraints[start+i] = declarations[i].uniqueConstraint
		d.uniqueConstraints[start+i].IsLocal = true
		if source := declarations[i].source; source != nil {
			stage.captureLikeSource(source)
		}
	}
	local := make(map[string]bool)
	for _, c := range d.notNullConstraints {
		local[c.Name] = true
	}
	for _, c := range d.checkConstraints {
		local[c.Name] = true
	}
	for _, c := range d.uniqueConstraints[:start] {
		local[c.Name] = true
	}
	for _, c := range d.exclusionConstraints {
		local[c.Name] = true
	}
	for _, c := range d.foreignKeys {
		local[c.Name] = true
	}
	view := e.stageRead(stage)
	used := constraintNames(view, d.namespace, nil)
	for name := range local {
		used[name] = true
	}
	for key := range view.state.names {
		if key.ClassID == RelationClassID && key.Namespace == d.namespace {
			used[key.Name] = true
		}
	}
	used[d.name] = true
	indices := make([]*relationDescriptor, 0, len(declarations))
	for i := start; i < len(d.uniqueConstraints); i++ {
		c := &d.uniqueConstraints[i]
		if c.Name == d.name {
			return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
		}
		if c.Name == "" {
			if c.Primary {
				c.Name = generatedConstraintName(d.name, "", "pkey", used)
			} else {
				labels := make([]string, len(c.Attributes))
				for j, a := range c.Attributes {
					labels[j] = d.columns[slices.Index(d.attributes, a)].Name
				}
				c.Name = generatedConstraintName(d.name, strings.Join(labels, "_"), "key", used)
			}
		}
		// An existing backing relation wins over a same-named local NN/CHECK
		// for PK. Ordinary UNIQUE retains its existing collision precedence.
		if c.Primary || altering {
			key := NameKey{ClassID: RelationClassID, Namespace: d.namespace, Name: c.Name}
			if stage.lookupName(key) != (ObjectAddress{}) {
				return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
			}
		}
		if local[c.Name] {
			return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, c.Name, d.name))
		}
		// Two distinct UNIQUE definitions with the same name collide as relations.
		key := NameKey{ClassID: RelationClassID, Namespace: d.namespace, Name: c.Name}
		if stage.lookupName(key) != (ObjectAddress{}) {
			return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
		}
		for _, index := range indices {
			if index.name == c.Name {
				return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
			}
		}
		used[c.Name] = true
		var err error
		c.OID, err = e.allocateOIDLocked()
		if err != nil {
			return err
		}
		id, err := e.allocateOIDLocked()
		if err != nil {
			return err
		}
		c.Index = ObjectAddress{ClassID: RelationClassID, ObjectID: id}
		revision, err := e.revisionLocked()
		if err != nil {
			return err
		}
		definition := &indexDefinition{method: btreeMethod, parent: d.address, unique: true, nullsNotDistinct: c.NullsNotDistinct, constraint: uniqueBacking(c), ready: true, valid: true, live: true}
		index := &relationDescriptor{address: c.Index, namespace: d.namespace, name: c.Name, revision: revision, owner: d.owner, relationKind: indexRelation, sqlIndex: definition}
		if source := declarations[i-start].source; source != nil {
			// LIKE clones the actual key providers and presentation, not a
			// freshly defaulted approximation or any source storage/root.
			*definition = *source.sqlIndex
			definition.parent, definition.constraint = d.address, uniqueBacking(c)
			definition.partitionParent = ObjectAddress{}
			definition.ready, definition.valid, definition.live = true, true, true
			definition.keys = slices.Clone(source.sqlIndex.keys)
			for j := range definition.keys {
				key := &definition.keys[j]
				if key.derived == nil {
					key.attribute = c.Attributes[j]
				} else {
					node, err := declarations[i-start].like.remap(key.derived.definition, d, stage.base)
					if err != nil {
						return err
					}
					execution, err := declarations[i-start].like.remap(key.derived.execution, d, stage.base)
					if err != nil {
						return err
					}
					derived := &derivedIndexKey{definition: node, execution: execution, attributes: d.attributes}
					if _, err := (scalarRenderer{aclOutput: capturedACLItemOutput{owner: e, roles: e.roleRead(stage.base, e.operationRolesLocked(op))}, owner: d, dependencies: &derived.dependencies}).sql(op.Context(), stage.base, node); err != nil {
						return err
					}
					key.derived = derived
				}
			}
			definition.include = make([]int32, len(source.sqlIndex.include))
			for j, attribute := range source.sqlIndex.include {
				definition.include[j], err = declarations[i-start].like.attribute(d, attribute)
				if err != nil {
					return err
				}
			}
			index.columns = slices.Clone(source.columns)
			index.attributes = slices.Clone(source.attributes)
			index.attributeHighWater = source.attributeHighWater
			if declarations[i-start].copyComments {
				index.comment = source.comment
			}
		} else {
			definition.keys = make([]indexKey, len(c.Attributes))
			index.columns = make([]ColumnDescriptor, len(c.Attributes))
			index.attributes = make([]int32, len(c.Attributes))
			index.attributeHighWater = int32(len(c.Attributes))
			for j, a := range c.Attributes {
				column := d.columns[slices.Index(d.attributes, a)]
				class, ok := sqltype.DefaultIndexClass(column.Usage.Type)
				if !ok {
					return indexDefaultClassError(column.Usage.Type.DiagnosticName(), btreeMethod)
				}
				definition.keys[j] = indexKey{attribute: a, class: class}
				column.Nullable, column.NullabilityKnown = true, true
				index.columns[j] = column
				index.attributes[j] = int32(j + 1)
			}
		}
		index.index = make(map[string]int, len(index.columns))
		for j := range index.columns {
			column := &index.columns[j]
			column.OriginTable = uint32(id)
			column.OriginAttribute = int16(j + 1)
			index.index[column.Name] = j
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
func uniqueKeyDetailAllowed(auth AuthorizationView, ref ObjectRef, keys []indexKey) (bool, error) {
	// RLS applicability is captured metadata/role authority, not a policy-row
	// evaluation. Table SELECT alone cannot reveal a hidden physical key.
	descriptor, ok := auth.catalog.Descriptor(ref.Address)
	parent, relation := descriptor.(*relationDescriptor)
	if !ok || !relation {
		return false, fmt.Errorf("stuntgres: unique disclosure parent is missing")
	}
	decision, err := rowSecurityDecision(auth, parent, auth.state.CurrentUser, policySelect, false)
	if err != nil || decision != rlsUnrestricted {
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
func uniqueKeyOutput(ctx context.Context, value sqltype.Value, class sqltype.IndexClass, catalog CatalogRead, output sqltype.OutputEnvironment) (string, error) {
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
func uniqueDiagnostic(ctx context.Context, parent, index *relationDescriptor, key []sqltype.Value, schema string, output sqltype.OutputEnvironment, detailAllowed, building bool, catalog CatalogRead) error {
	message := fmt.Sprintf(`duplicate key value violates unique constraint "%s"`, index.name)
	if building {
		message = fmt.Sprintf(`could not create unique index "%s"`, index.name)
	}
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: message, Schema: schema, Table: parent.name, Constraint: index.name}
	if detailAllowed {
		labels := make([]string, len(key))
		values := make([]string, len(key))
		for i, value := range key {
			attribute, node, err := indexCatalogKey(parent, index.sqlIndex.keys[i])
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
				text, err := uniqueKeyOutput(ctx, value, index.sqlIndex.keys[i].class, catalog, output)
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

func validateUniqueRoot(ctx context.Context, parent, index *relationDescriptor, root *logicalIndexRoot, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool) error {
	d := index.sqlIndex
	if d.isExclusion() {
		return validateExclusionRoot(ctx, parent, index, root, schema, output, auth, building)
	}
	if !d.unique {
		return nil
	}
	ordering, err := bindIndexOrdering(parent, d)
	if err != nil {
		return err
	}
	return validateUniqueEntries(ctx, parent, index, root.entries, schema, output, auth, building, ordering)
}

func validateUniqueEntries(ctx context.Context, parent, index *relationDescriptor, entries []logicalIndexEntry, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool, ordering indexOrdering) error {
	if index.sqlIndex.isExclusion() {
		return validateExclusionEntries(ctx, parent, index, entries, schema, output, auth, building)
	}
	for i := 1; i < len(entries); i++ {
		if err := validateUniquePair(ctx, parent, index, entries[i-1], entries[i], schema, output, auth, building, ordering); err != nil {
			return err
		}
	}
	return nil
}

func validateUniquePair(ctx context.Context, parent, index *relationDescriptor, previous, current logicalIndexEntry, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool, ordering indexOrdering) error {
	d := index.sqlIndex
	if d.isExclusion() {
		return validateExclusionPair(ctx, parent, index, previous, current, schema, output, auth, building)
	}
	if !d.unique || !d.nullsNotDistinct && uniqueHasNull(current.keys) {
		return nil
	}
	cmp, err := ordering.compareKeys(ctx, previous, current)
	if err != nil {
		return err
	}
	if cmp == 0 {
		allowed := auth == nil || auth.engine == nil // Trusted publication has no SQL principal.
		if auth != nil && auth.engine != nil {
			ref := ObjectRef{owner: auth.catalog.owner.domain, Epoch: auth.catalog.Epoch(), Address: parent.address}
			allowed, err = uniqueKeyDetailAllowed(*auth, ref, d.keys)
			if err != nil {
				return err
			}
		}
		key := current.keys
		if building {
			// Report an actual earlier comparator tuple for builds, not a
			// normalized equality value. Validation reports the candidate.
			key = previous.keys
		}
		var catalog CatalogRead
		if auth != nil {
			catalog = auth.catalog
		}
		return uniqueDiagnostic(ctx, parent, index, key, schema, output, allowed, building, catalog)
	}
	return nil
}

func (r pgRenderer) uniqueConstraintSQL(parent *relationDescriptor, c *uniqueConstraint) (string, error) {
	index, ok := r.view.state.descriptors[c.Index].(*relationDescriptor)
	if !ok || index.sqlIndex == nil {
		return "", fmt.Errorf("stuntgres: missing unique deparse index")
	}
	labels := make([]string, len(c.Attributes)+len(index.sqlIndex.include))
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
	if len(index.sqlIndex.include) != 0 {
		for i, attribute := range index.sqlIndex.include {
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
