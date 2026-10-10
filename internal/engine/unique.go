package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

// Constraint and backing relation are separate identities published together.
type uniqueConstraint struct {
	OID              OID
	Name             string
	Attributes       []int32
	NullsNotDistinct bool
	Index            ObjectAddress
}

func validateUniques(view CatalogRead, parent *relationDescriptor, names map[string]bool) error {
	for _, c := range parent.uniqueConstraints {
		if parent.relationKind != tableRelation || c.OID == 0 || c.Name == "" || len(c.Name) > 63 || !utf8.ValidString(c.Name) || strings.IndexByte(c.Name, 0) >= 0 || names[c.Name] || len(c.Attributes) == 0 || len(c.Attributes) > 32 {
			return fmt.Errorf("stuntgres: invalid unique constraint identity")
		}
		names[c.Name] = true
		index, ok := view.state.descriptors[c.Index].(*relationDescriptor)
		if !ok || index.sqlIndex == nil || index.name != c.Name || index.sqlIndex.parent != parent.address || !index.sqlIndex.unique || index.sqlIndex.constraint == nil || index.sqlIndex.constraint.OID != c.OID || index.sqlIndex.constraint.Index != c.Index || index.sqlIndex.constraint.Name != c.Name || index.sqlIndex.nullsNotDistinct != c.NullsNotDistinct || !slices.Equal(index.sqlIndex.constraint.Attributes, c.Attributes) || len(index.sqlIndex.keys) != len(c.Attributes) {
			return fmt.Errorf("stuntgres: inconsistent unique backing index")
		}
		used := make(map[int32]bool, len(c.Attributes))
		for i, a := range c.Attributes {
			if used[a] || !parent.hasAttribute(a) || index.sqlIndex.keys[i].attribute != a || index.sqlIndex.keys[i].desc || index.sqlIndex.keys[i].nullsFirst {
				return fmt.Errorf("stuntgres: invalid unique key")
			}
			used[a] = true
		}
	}
	return nil
}

func (e *Engine) stageCreateUniques(stage *CatalogStage, d *relationDescriptor, s *parser.CreateTable) error {
	var declarations []parser.UniqueConstraint
	for _, element := range s.Elements {
		if element.Unique != nil {
			declarations = append(declarations, *element.Unique)
		}
		if element.Column != nil {
			for _, constraint := range element.Column.Constraints {
				if constraint.Unique != nil {
					c := *constraint.Unique
					c.Columns = []parser.Identifier{{Text: element.Column.Name, Pos: element.Column.Pos}}
					declarations = append(declarations, c)
				}
			}
		}
	}
	for _, raw := range declarations {
		if len(raw.Columns) > 32 {
			return sqlError("54011", "cannot use more than 32 columns in an index")
		}
		c := uniqueConstraint{Name: raw.Name.Text, NullsNotDistinct: raw.NullsNotDistinct, Attributes: make([]int32, len(raw.Columns))}
		seen := make(map[int32]bool, len(raw.Columns))
		for i, column := range raw.Columns {
			slot, ok := d.index[column.Text]
			if !ok {
				return insertError("42703", fmt.Sprintf(`column "%s" named in key does not exist`, column.Text), raw.Pos)
			}
			attr := d.attributes[slot]
			if seen[attr] {
				return insertError("42701", fmt.Sprintf(`column "%s" appears twice in unique constraint`, column.Text), raw.Pos)
			}
			seen[attr] = true
			c.Attributes[i] = attr
		}
		redundant := false
		for i, old := range d.uniqueConstraints {
			if old.NullsNotDistinct == c.NullsNotDistinct && slices.Equal(old.Attributes, c.Attributes) {
				if old.Name == "" {
					d.uniqueConstraints[i].Name = c.Name
				}
				redundant = true
				break
			}
		}
		if !redundant {
			d.uniqueConstraints = append(d.uniqueConstraints, c)
		}
	}
	local := make(map[string]bool)
	for _, c := range d.notNullConstraints {
		local[c.Name] = true
	}
	for _, c := range d.checkConstraints {
		local[c.Name] = true
	}
	used := constraintNames(e.stageRead(stage), d.namespace, nil)
	for name := range local {
		used[name] = true
	}
	for key := range stage.base.state.names {
		if key.ClassID == RelationClassID && key.Namespace == d.namespace {
			used[key.Name] = true
		}
	}
	for key := range stage.names {
		if key.ClassID == RelationClassID && key.Namespace == d.namespace {
			used[key.Name] = true
		}
	}
	used[d.name] = true
	indices := make([]*relationDescriptor, 0, len(d.uniqueConstraints))
	for i := range d.uniqueConstraints {
		c := &d.uniqueConstraints[i]
		if c.Name == d.name {
			return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
		}
		if c.Name == "" {
			labels := make([]string, len(c.Attributes))
			for j, a := range c.Attributes {
				for slot, attr := range d.attributes {
					if attr == a {
						labels[j] = d.columns[slot].Name
						break
					}
				}
			}
			c.Name = generatedConstraintName(d.name, strings.Join(labels, "_"), "key", used)
		}
		if local[c.Name] {
			return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, c.Name, d.name))
		}
		// Two distinct UNIQUE definitions with the same name collide as relations.
		key := NameKey{ClassID: RelationClassID, Namespace: d.namespace, Name: c.Name}
		if _, ok := stage.base.state.names[key]; ok {
			return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
		}
		if _, ok := stage.names[key]; ok {
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
		definition := &indexDefinition{method: btreeMethod, parent: d.address, unique: true, nullsNotDistinct: c.NullsNotDistinct, constraint: c, keys: make([]indexKey, len(c.Attributes)), ready: true, valid: true, live: true}
		index := &relationDescriptor{address: c.Index, namespace: d.namespace, name: c.Name, revision: revision, owner: d.owner, relationKind: indexRelation, sqlIndex: definition, columns: make([]ColumnDescriptor, len(c.Attributes)), attributes: make([]int32, len(c.Attributes)), index: make(map[string]int, len(c.Attributes))}
		index.attributeHighWater = int32(len(index.attributes))
		labels := make(map[string]bool)
		for j, a := range c.Attributes {
			slot := slices.Index(d.attributes, a)
			column := d.columns[slot]
			class, ok := sqltype.DefaultIndexClass(column.Usage.Type)
			if !ok {
				return indexDefaultClassError(column.Usage.Type.DiagnosticName(), btreeMethod)
			}
			definition.keys[j] = indexKey{attribute: a, class: class}
			label := column.Name
			for suffix := 1; labels[label]; suffix++ {
				label = indexColumnLabel(column.Name, suffix)
			}
			labels[label] = true
			column.Name = label
			column.Nullable = true
			column.NullabilityKnown = true
			column.OriginTable = uint32(id)
			column.OriginAttribute = int16(j + 1)
			index.columns[j] = column
			index.attributes[j] = int32(j + 1)
			index.index[label] = j
		}
		indices = append(indices, index)
	}
	if err := stage.Create(d); err != nil {
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
	rights, err := auth.Effective(ref, false)
	if err != nil {
		return false, err
	}
	if rights&SelectPrivilege != 0 {
		return true, nil
	}
	descriptor, ok := auth.catalog.Descriptor(ref.Address)
	parent, relation := descriptor.(*relationDescriptor)
	if !ok || !relation {
		return false, fmt.Errorf("stuntgres: unique disclosure parent is missing")
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
		if rights&SelectPrivilege == 0 {
			return false, nil
		}
	}
	return true, nil
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
				text, err := value.Type().OutputWithEnvironment(ctx, value, output)
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
	for i := 1; i < len(entries); i++ {
		if err := validateUniquePair(ctx, parent, index, entries[i-1], entries[i], schema, output, auth, building, ordering); err != nil {
			return err
		}
	}
	return nil
}

func validateUniquePair(ctx context.Context, parent, index *relationDescriptor, previous, current logicalIndexEntry, schema string, output sqltype.OutputEnvironment, auth *AuthorizationView, building bool, ordering indexOrdering) error {
	d := index.sqlIndex
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
			ref := ObjectRef{owner: auth.catalog.owner.owner, Epoch: auth.catalog.Epoch(), Address: parent.address}
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

func uniqueConstraintSQL(parent *relationDescriptor, c *uniqueConstraint) (string, error) {
	labels := make([]string, len(c.Attributes))
	for i, attribute := range c.Attributes {
		label, err := deparseAttribute(parent, attribute)
		if err != nil {
			return "", err
		}
		labels[i] = label
	}
	prefix := "UNIQUE"
	if c.NullsNotDistinct {
		prefix += " NULLS NOT DISTINCT"
	}
	return prefix + " (" + strings.Join(labels, ", ") + ")", nil
}
