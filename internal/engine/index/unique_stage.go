package index

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
)

// CREATE and ALTER share index/provider/ownership construction. ALTER appends
// only new groups and replaces the existing parent instead of creating it.
//
// The caller records each declaration's LIKE source first.
func StageUniqueDefinitions(h Host, d *catalog.Relation, declarations []analyze.UniqueConstraint, altering bool) error {
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
	view := h.StageRead()
	used := catalog.ConstraintNames(view, d.Namespace(), nil)
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
				c.Name = catalog.GeneratedConstraintName(d.Name(), "", "pkey", used)
			} else {
				labels := make([]string, len(c.Attributes))
				for j, a := range c.Attributes {
					labels[j] = d.Columns[slices.Index(d.Attributes, a)].Name
				}
				c.Name = catalog.GeneratedConstraintName(d.Name(), strings.Join(labels, "_"), "key", used)
			}
		}
		// An existing backing relation wins over a same-named local NN/CHECK
		// for PK. Ordinary UNIQUE retains its existing collision precedence.
		if c.Primary || altering {
			key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: c.Name}
			if h.Stage().LookupName(key) != (ids.ObjectAddress{}) {
				return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
			}
		}
		if local[c.Name] {
			return sqlError("42710", fmt.Sprintf(`constraint "%s" for relation "%s" already exists`, c.Name, d.Name()))
		}
		// Two distinct UNIQUE definitions with the same name collide as relations.
		key := ids.NameKey{ClassID: ids.RelationClassID, Namespace: d.Namespace(), Name: c.Name}
		if h.Stage().LookupName(key) != (ids.ObjectAddress{}) {
			return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
		}
		for _, staged := range indices {
			if staged.Name() == c.Name {
				return sqlError("42P07", fmt.Sprintf(`relation "%s" already exists`, c.Name))
			}
		}
		used[c.Name] = true
		var err error
		c.OID, err = h.AllocateOID()
		if err != nil {
			return err
		}
		id, err := h.AllocateOID()
		if err != nil {
			return err
		}
		c.Index = ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: id}
		revision, err := h.Revision()
		if err != nil {
			return err
		}
		definition := &catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: 403, Parent: d.Address(), Unique: true, NullsNotDistinct: c.NullsNotDistinct, Constraint: UniqueBacking(c), Ready: true, Valid: true, Live: true}
		idx := &catalog.Relation{ObjectAddress: c.Index, ObjectNamespace: d.Namespace(), ObjectName: c.Name, ObjectRevision: revision, Owner: d.Owner, RelationKind: catalog.IndexRelation, SQLIndex: definition}
		if source := declarations[i-start].Source; source != nil {
			// LIKE clones the actual key providers and presentation, not a
			// freshly defaulted approximation or any source storage/root.
			*definition = *source.SQLIndex
			definition.Parent, definition.Constraint = d.Address(), UniqueBacking(c)
			definition.PartitionParent = ids.ObjectAddress{}
			definition.Ready, definition.Valid, definition.Live = true, true, true
			definition.Keys = slices.Clone(source.SQLIndex.Keys)
			for j := range definition.Keys {
				key := &definition.Keys[j]
				if key.Derived == nil {
					key.Attribute = c.Attributes[j]
				} else {
					node, err := stored.RemapLikeExpression(declarations[i-start].Like, key.Derived.Definition, d, h.StageBase())
					if err != nil {
						return err
					}
					execution, err := stored.RemapLikeExpression(declarations[i-start].Like, key.Derived.Execution, d, h.StageBase())
					if err != nil {
						return err
					}
					derived := &catalog.DerivedIndexKey{Definition: node, Execution: execution, Attributes: d.Attributes}
					if _, err := (stored.ScalarRenderer{ACLOutput: h.ACLOutput(h.StageBase()), Owner: d, Dependencies: &derived.Dependencies}).SQL(h.Context(), h.StageBase(), node); err != nil {
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
			idx.Columns = slices.Clone(source.Columns)
			idx.Attributes = slices.Clone(source.Attributes)
			idx.ColumnMeta.HighWater = source.ColumnMeta.HighWater
			if declarations[i-start].CopyComments {
				idx.Comment = source.Comment
			}
		} else {
			definition.Keys = make([]catalog.IndexKey, len(c.Attributes))
			idx.Columns = make([]catalog.Column, len(c.Attributes))
			idx.Attributes = make([]int32, len(c.Attributes))
			idx.ColumnMeta.HighWater = int32(len(c.Attributes))
			for j, a := range c.Attributes {
				column := d.Columns[slices.Index(d.Attributes, a)]
				class, ok, err := h.StageBase().DefaultIndexClass(definition.MethodIdentity(), column.Usage.Type)
				if err != nil {
					return err
				}
				if !ok {
					return DefaultClassError(column.Usage.Type.DiagnosticName(), familyMethodName(h.StageBase(), definition.MethodIdentity()))
				}
				definition.Keys[j] = catalog.IndexKey{Attribute: a, Class: class}
				column.Nullable, column.NullabilityKnown = true, true
				idx.Columns[j] = column
				idx.Attributes[j] = int32(j + 1)
			}
		}
		if err := CopyColumnStorage(h.StageRead(), d, idx); err != nil { return err }
		idx.Index = make(map[string]int, len(idx.Columns))
		for j := range idx.Columns {
			column := &idx.Columns[j]
			column.OriginTable = uint32(id)
			column.OriginAttribute = int16(j + 1)
			idx.Index[column.Name] = j
		}
		indices = append(indices, idx)
	}
	var err error
	if altering {
		err = h.Stage().Replace(d)
	} else {
		err = h.Stage().Create(d)
	}
	if err != nil {
		return err
	}
	for _, staged := range indices {
		if err := h.Stage().Create(staged); err != nil {
			return err
		}
	}
	return nil
}

// StageCreateUniques stages the UNIQUE and PRIMARY KEY constraints of a new
// table d with their backing indexes.
func StageCreateUniques(h Host, d *catalog.Relation, declarations []analyze.UniqueConstraint) error {
	return StageUniqueDefinitions(h, d, declarations, false)
}
