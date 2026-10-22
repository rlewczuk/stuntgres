package index

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
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
)

func PhysicalNotNullOriginValid(view catalog.Read, table *catalog.Relation, attribute int32, proof catalog.PhysicalNotNullProof) bool {
	cause := proof.Cause
	standalone := catalog.PhysicalNotNullStandaloneCause(cause)
	if cause == nil || cause.Self != cause || cause.Kind != catalog.PhysicalNotNullTypeCleanup && cause.Kind != catalog.PhysicalNotNullImportCleanup && !standalone || cause.Ref.Address != table.Address() || cause.Ref.Address.SubID != 0 || cause.Attribute != attribute || cause.RemovedNN == 0 || cause.Usage.Type == nil || !stored.ReferenceEpochValid(view, table, cause.Ref) {
		return false
	}
	if cause.Origin.Domain() == nil || cause.Origin.Epoch == 0 || cause.Origin.Address.ClassID != ids.RelationClassID || cause.Origin.Address.ObjectID == 0 || cause.Origin.Address.SubID != 0 {
		return false
	}
	if standalone {
		if cause.Primary != 0 || cause.Index != (ids.ObjectAddress{}) {
			return false
		}
	} else if cause.Primary == 0 || cause.Index.ClassID != ids.RelationClassID || cause.Index.ObjectID == 0 || cause.Index.SubID != 0 {
		return false
	}
	slot := table.IndexForAttribute(attribute)
	return table.RelationKind == catalog.TableRelation && table.Partition == nil && table.Membership == nil && slot >= 0 && table.Columns[slot].NullabilityKnown && !table.Columns[slot].Nullable && proof.Usage == table.Columns[slot].Usage
}

func PhysicalPrimarySupportValid(view catalog.Read, table *catalog.Relation, primary *catalog.UniqueConstraint, support *catalog.PhysicalPrimarySupport) bool {
	if support == nil || primary == nil || !primary.Primary || support.Constraint != primary.OID || support.Index != primary.Index || len(support.Keys) != len(primary.Attributes) {
		return false
	}
	index, ok := view.Relation(primary.Index)
	if !ok || index.RelationKind != catalog.IndexRelation || index.Namespace() != table.Namespace() || index.Owner != table.Owner || index.SQLIndex == nil || index.SQLIndex.Parent != table.Address() || index.SQLIndex.Constraint == nil || index.SQLIndex.Constraint.Kind != catalog.UniqueBackingKind || index.SQLIndex.Constraint.OID != primary.OID || index.SQLIndex.Constraint.Index != primary.Index || index.SQLIndex.Constraint.Name != primary.Name || !index.SQLIndex.Constraint.Primary || len(index.SQLIndex.Keys) != len(support.Keys) || len(index.Columns) < len(support.Keys) {
		return false
	}
	for i, key := range support.Keys {
		slot := table.IndexForAttribute(key.Attribute)
		attribute, expression, err := CatalogKey(table, index.SQLIndex.Keys[i])
		if slot < 0 || key.Attribute != primary.Attributes[i] || key.Usage != table.Columns[slot].Usage || key.Usage != index.Columns[i].Usage || err != nil || expression != nil || attribute != key.Attribute {
			return false
		}
	}
	return true
}

func ValidateUniques(view catalog.Read, parent *catalog.Relation, names map[string]bool) error {
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
			attribute, expression, err := CatalogKey(parent, index.SQLIndex.Keys[i])
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
					if !exists || !PhysicalNotNullOriginValid(view, parent, a, proof) || proof.Support == nil {
						return fmt.Errorf("stuntgres: missing primary not-null identity")
					}
					if physicalSupport != proof.Support {
						if !PhysicalPrimarySupportValid(view, parent, &c, proof.Support) {
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

func UniqueHasNull(key []sqltype.Value) bool {
	for _, v := range key {
		if v.IsNull() {
			return true
		}
	}
	return false
}

// DDL ownership is independent of ordinary key SELECT disclosure rights.
func UniqueKeyDetailAllowed(view access.View, env access.Environment, ref ids.ObjectRef, keys []catalog.IndexKey) (bool, error) {
	// RLS applicability is captured metadata/role authority, not a policy-row
	// evaluation. Table SELECT alone cannot reveal a hidden physical key.
	descriptor, ok := view.Catalog.Descriptor(ref.Address)
	parent, relation := descriptor.(*catalog.Relation)
	if !ok || !relation {
		return false, fmt.Errorf("stuntgres: unique disclosure parent is missing")
	}
	decision, err := access.RowSecurityDecision(view.Roles, parent, view.State.CurrentUser, catalog.PolicySelect, false)
	if err != nil || decision != access.RLSUnrestricted {
		return false, err
	}
	rights, err := view.Effective(env, ref, false)
	if err != nil {
		return false, err
	}
	if rights&authorization.SelectPrivilege != 0 {
		return true, nil
	}
	for _, key := range keys {
		attribute, expression, err := CatalogKey(parent, key)
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
		rights, err := view.Effective(env, column, false)
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
func UniqueKeyOutput(ctx context.Context, value sqltype.Value, class sqltype.IndexClass, catalog catalog.Read, output sqltype.OutputEnvironment) (string, error) {
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
func UniqueDiagnostic(ctx context.Context, parent, index *catalog.Relation, key []sqltype.Value, schema string, output sqltype.OutputEnvironment, detailAllowed, building bool, catalog catalog.Read) error {
	message := fmt.Sprintf(`duplicate key value violates unique constraint "%s"`, index.Name())
	if building {
		message = fmt.Sprintf(`could not create unique index "%s"`, index.Name())
	}
	d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "23505", Message: message, Schema: schema, Table: parent.Name(), Constraint: index.Name()}
	if detailAllowed {
		labels := make([]string, len(key))
		values := make([]string, len(key))
		for i, value := range key {
			attribute, node, err := CatalogKey(parent, index.SQLIndex.Keys[i])
			if err != nil {
				return err
			}
			if node != nil {
				labels[i], err = (stored.Deparser{View: catalog, Relation: parent, Output: output}).SQL(ctx, node, stored.DeparseParent{})
			} else {
				labels[i], err = stored.DeparseAttribute(parent, attribute)
			}
			if err != nil {
				return err
			}
			if value.IsNull() {
				values[i] = "null"
			} else {
				text, err := UniqueKeyOutput(ctx, value, index.SQLIndex.Keys[i].Class, catalog, output)
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

// ValidateUniqueRoot always uses view's catalog for support binding. A nil env,
// not a nil view, marks trusted publication without a SQL disclosure principal.
func ValidateUniqueRoot(ctx context.Context, parent, index *catalog.Relation, root *storage.IndexRoot, schema string, output sqltype.OutputEnvironment, view *access.View, env access.Environment, building bool) error {
	d := index.SQLIndex
	if d.IsExclusion() {
		return ValidateExclusionRoot(ctx, parent, index, root, schema, output, view, env, building)
	}
	if !d.Unique {
		return nil
	}
	ordering, err := BindIndexOrdering(view.Catalog, parent, d)
	if err != nil {
		return err
	}
	return ValidateUniqueEntries(ctx, parent, index, root.Entries, schema, output, view, env, building, ordering)
}

func ValidateUniqueEntries(ctx context.Context, parent, index *catalog.Relation, entries []storage.IndexEntry, schema string, output sqltype.OutputEnvironment, view *access.View, env access.Environment, building bool, ordering Ordering) error {
	if index.SQLIndex.IsExclusion() {
		return validateExclusionEntries(ctx, parent, index, entries, schema, output, view, env, building)
	}
	for i := 1; i < len(entries); i++ {
		if err := validateUniquePair(ctx, parent, index, entries[i-1], entries[i], schema, output, view, env, building, ordering); err != nil {
			return err
		}
	}
	return nil
}

func validateUniquePair(ctx context.Context, parent, index *catalog.Relation, previous, current storage.IndexEntry, schema string, output sqltype.OutputEnvironment, view *access.View, env access.Environment, building bool, ordering Ordering) error {
	d := index.SQLIndex
	if d.IsExclusion() {
		return ValidateExclusionPair(ctx, parent, index, previous, current, schema, output, view, env, building)
	}
	if !d.Unique || !d.NullsNotDistinct && UniqueHasNull(current.Keys) {
		return nil
	}
	cmp, err := ordering.CompareKeys(ctx, previous, current)
	if err != nil {
		return err
	}
	if cmp == 0 {
		allowed := env == nil // Trusted publication has no SQL principal.
		if env != nil {
			ref := view.Catalog.Owner().Domain.Ref(view.Catalog.Epoch(), parent.Address())
			allowed, err = UniqueKeyDetailAllowed(*view, env, ref, d.Keys)
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
		if view != nil {
			catalog = view.Catalog
		}
		return UniqueDiagnostic(ctx, parent, index, key, schema, output, allowed, building, catalog)
	}
	return nil
}
