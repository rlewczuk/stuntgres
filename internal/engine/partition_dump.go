package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func partitionDumpPredecessors(catalog CatalogRead, d *relationDescriptor, predecessors []ids.OID) ([]ids.OID, error) {
	add := func(address ids.ObjectAddress) error {
		id, err := dumpPredecessor(catalog, address, d.address)
		if err != nil {
			return err
		}
		if id != 0 && !slices.Contains(predecessors, id) {
			predecessors = append(predecessors, id)
		}
		return nil
	}
	if d.Membership != nil {
		if err := add(d.Membership.parent); err != nil {
			return nil, err
		}
		if d.Membership.defaultBound {
			parent := catalog.MustRelation(d.Membership.parent)
			for _, address := range parent.Partition.children {
				if address != d.address && !partitionExclusionDefaultBefore(catalog, d, catalog.MustRelation(address)) {
					if err := add(address); err != nil {
						return nil, err
					}
				}
			}
		}
		if !d.Membership.defaultBound {
			for _, tuple := range [2][]partitionDatum{d.Membership.lower, d.Membership.upper} {
				for _, datum := range tuple {
					if datum.kind != 0 {
						continue
					}
					literal := literalExpression{Info: expr.Info{Usage: datum.value.Type().Usage()}, value: datum.value}
					if err := regclassReplayReferences(catalog, literal, add); err != nil {
						return nil, err
					}
					var buffer [1]ids.ObjectRef
					references := buffer[:0]
					if err := (scalarRenderer{dependencies: &references}).recordCatalogAliasReferences(catalog, datum.value); err != nil {
						return nil, err
					}
					if err := (scalarRenderer{dependencies: &references}).recordRoutineReferences(catalog, datum.value); err != nil {
						return nil, err
					}
					for _, ref := range references {
						if err := add(ref.Address); err != nil {
							return nil, err
						}
					}
				}
			}
		}
	}
	if d.Partition != nil {
		for _, key := range d.Partition.keys {
			if key.derived != nil {
				for _, ref := range key.derived.dependencies {
					if err := add(ref.Address); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return predecessors, nil
}

func partitionKeySQL(ctx context.Context, catalog CatalogRead, d *relationDescriptor) (string, error) {
	if d.Partition == nil {
		return "", nil
	}
	var text strings.Builder
	if d.Partition.strategy == parser.PartitionHash {
		text.WriteString(" PARTITION BY HASH (")
	} else {
		text.WriteString(" PARTITION BY RANGE (")
	}
	for i, key := range d.Partition.keys {
		if i != 0 {
			text.WriteString(", ")
		}
		if key.derived != nil {
			expression, err := (scalarRenderer{owner: d}).sql(ctx, catalog, key.derived.definition)
			if err != nil {
				return "", err
			}
			text.WriteString("(" + expression + ")")
		} else {
			slot := slices.Index(d.Attributes, key.attribute)
			if slot < 0 {
				return "", fmt.Errorf("stuntgres: missing partition key attribute")
			}
			text.WriteString(quoteIdentifier(d.Columns[slot].Name))
		}
		if key.collation != 0 {
			name := collationSQLName(catalog, key.collation)
			if name == "" {
				return "", fmt.Errorf("stuntgres: unsupported stored collation")
			}
			text.WriteString(" COLLATE " + name)
		}
		if key.explicitClass {
			text.WriteString(" " + quoteIdentifier("pg_catalog") + "." + quoteIdentifier(key.class.Name))
		}
	}
	text.WriteString(")")
	return text.String(), nil
}

// PARTITION OF copies rank zero and inherits order, defaults and NOT NULL names.
// Existing-table attachment need not have any of those properties. Decide from
// captured definitions, not a historical CREATE/ATTACH flag, so logical replay
// remains canonical after stable attribute numbers have been reminted.
func partitionDumpNeedsAttach(catalog CatalogRead, d *relationDescriptor) bool {
	if d.Membership == nil {
		return false
	}
	parent := catalog.MustRelation(d.Membership.parent)
	for i, column := range d.Columns {
		if column.DeclaredDimensions != 0 || column.Name != parent.Columns[i].Name {
			return true
		}
		attribute, parentAttribute := d.Attributes[i], parent.Attributes[i]
		if _, exists := parent.Defaults[parentAttribute]; exists {
			if _, exists := d.Defaults[attribute]; !exists {
				return true
			}
		}
		for _, child := range d.NotNullConstraints {
			if child.Attribute != attribute {
				continue
			}
			if child.NotValid {
				return true
			}
			for _, inherited := range parent.NotNullConstraints {
				if inherited.Attribute == parentAttribute && child.Name != inherited.Name {
					return true
				}
			}
		}
	}
	for _, check := range d.CheckConstraints {
		if check.NotValid || check.NoInherit {
			return true
		}
	}
	return false
}

func partitionAttachDumpSQL(ctx context.Context, catalog CatalogRead, d *relationDescriptor) (string, error) {
	parent := catalog.MustRelation(d.Membership.parent)
	qualified := func(table *relationDescriptor) string {
		return quoteIdentifier(catalog.Get(table.namespace).Name()) + "." + quoteIdentifier(table.name)
	}
	text := "ALTER TABLE " + qualified(parent) + " ATTACH PARTITION " + qualified(d)
	if d.Membership.defaultBound {
		return text + " DEFAULT;\n", nil
	}
	if d.Membership.strategy == parser.PartitionHash {
		return text + hashPartitionBoundSQL(d.Membership) + ";\n", nil
	}
	lower, err := partitionTupleDumpSQL(ctx, catalog, parent.Partition.keys, d.Membership.lower)
	if err != nil {
		return "", err
	}
	upper, err := partitionTupleDumpSQL(ctx, catalog, parent.Partition.keys, d.Membership.upper)
	if err != nil {
		return "", err
	}
	return text + " FOR VALUES FROM (" + lower + ") TO (" + upper + ");\n", nil
}

// PARTITION OF names no new column types. Inherited definitions are supplied
// by the parent; only local default/nullability/check differences are emitted.
func partitionChildSQL(ctx context.Context, catalog CatalogRead, d *relationDescriptor, qualified string, deferred map[int32]bool) (string, error) {
	parent, ok := catalog.Relation(d.Membership.parent)
	if !ok || parent.Partition == nil {
		return "", fmt.Errorf("stuntgres: missing partition dump parent")
	}
	var text strings.Builder
	text.WriteString("CREATE TABLE " + qualified + " PARTITION OF " + quoteIdentifier(catalog.Get(parent.namespace).Name()) + "." + quoteIdentifier(parent.name))
	var options strings.Builder
	appendOption := func(option string) {
		if options.Len() != 0 {
			options.WriteString(", ")
		}
		options.WriteString(option)
	}
	for i, column := range d.Columns {
		var option strings.Builder
		option.WriteString(quoteIdentifier(column.Name))
		changed := false
		childDefault, childHasDefault := d.Defaults[d.Attributes[i]]
		parentDefault, parentHasDefault := parent.Defaults[parent.Attributes[i]]
		childSQL, parentSQL := "NULL", "NULL"
		var err error
		if childHasDefault {
			childSQL, err = defaultSQL(ctx, catalog, childDefault.expression)
			if err != nil {
				return "", err
			}
		}
		if parentHasDefault {
			parentSQL, err = defaultSQL(ctx, catalog, parentDefault.expression)
			if err != nil {
				return "", err
			}
		}
		if !deferred[d.Attributes[i]] && (childHasDefault != parentHasDefault || childSQL != parentSQL) {
			option.WriteString(" DEFAULT " + childSQL)
			changed = true
		}
		if !column.Nullable {
			for _, constraint := range d.NotNullConstraints {
				if constraint.Attribute == d.Attributes[i] && (parent.Columns[i].Nullable || constraint.partitionLocal) {
					option.WriteString(" CONSTRAINT " + quoteIdentifier(constraint.Name) + " NOT NULL")
					changed = true
					break
				}
			}
		}
		if changed {
			appendOption(option.String())
		}
	}
	for _, constraint := range orderedChecks(d) {
		expression, err := (scalarRenderer{owner: d, checkReplayPrecision: true}).sql(ctx, catalog, constraint.expression)
		if err != nil {
			return "", err
		}
		inherited := false
		for _, ancestor := range parent.CheckConstraints {
			if ancestor.Name != constraint.Name {
				continue
			}
			parentSQL, err := (scalarRenderer{owner: parent, checkReplayPrecision: true}).sql(ctx, catalog, ancestor.expression)
			if err != nil {
				return "", err
			}
			inherited = parentSQL == expression && ancestor.NoInherit == constraint.NoInherit
		}
		if inherited {
			continue
		}
		option := "CONSTRAINT " + quoteIdentifier(constraint.Name) + " CHECK (" + expression + ")"
		if constraint.NoInherit {
			option += " NO INHERIT"
		}
		appendOption(option)
	}
	for _, constraint := range d.UniqueConstraints {
		if constraint.Parent != 0 {
			continue
		}
		index := catalog.MustRelation(constraint.Index)
		if partitionReplaySeparateConstraint(catalog, index) {
			continue
		}
		definition, err := (pgRenderer{view: catalog}).uniqueConstraintSQL(d, &constraint)
		if err != nil {
			return "", err
		}
		appendOption("CONSTRAINT " + quoteIdentifier(constraint.Name) + " " + definition)
	}
	for _, constraint := range d.ExclusionConstraints {
		if constraint.Parent != 0 {
			continue
		}
		index, ok := catalog.Relation(constraint.Index)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing exclusion dump index")
		}
		if partitionReplaySeparateConstraint(catalog, index) {
			continue
		}
		definition, err := indexDefinitionSQL(ctx, catalog, index, indexPresentation{dump: true, exclusion: true})
		if err != nil {
			return "", err
		}
		appendOption("CONSTRAINT " + quoteIdentifier(constraint.Name) + " " + definition)
	}
	if options.Len() != 0 {
		text.WriteString(" (" + options.String() + ")")
	}
	if d.Membership.defaultBound {
		text.WriteString(" DEFAULT")
	} else if d.Membership.strategy == parser.PartitionHash {
		text.WriteString(hashPartitionBoundSQL(d.Membership))
	} else {
		lower, err := partitionTupleDumpSQL(ctx, catalog, parent.Partition.keys, d.Membership.lower)
		if err != nil {
			return "", err
		}
		upper, err := partitionTupleDumpSQL(ctx, catalog, parent.Partition.keys, d.Membership.upper)
		if err != nil {
			return "", err
		}
		text.WriteString(" FOR VALUES FROM (" + lower + ") TO (" + upper + ")")
	}
	return text.String(), nil
}

// Bounds are evaluated datums, not saved SQL expressions. The ordinary Dump
// literal authority preserves owned type declarations and reference names.
func partitionTupleDumpSQL(ctx context.Context, catalog CatalogRead, keys []partitionKeyEntry, tuple []partitionDatum) (string, error) {
	var text strings.Builder
	for i, datum := range tuple {
		if i != 0 {
			text.WriteString(", ")
		}
		switch datum.kind {
		case -1:
			text.WriteString("MINVALUE")
		case 1:
			text.WriteString("MAXVALUE")
		default:
			literal := literalExpression{Info: expr.Info{Usage: keys[i].usage}, value: datum.value}
			value, err := catalogLiteralSQL(ctx, catalog, literal)
			if err != nil {
				return "", err
			}
			text.WriteString(value)
		}
	}
	return text.String(), nil
}

func hashPartitionBoundSQL(bound *partitionMembership) string {
	return fmt.Sprintf(" FOR VALUES WITH (MODULUS %d, REMAINDER %d)", bound.modulus, bound.remainder)
}

// Replay identity-sensitive rows through their highest HASH ancestor; RANGE-only
// trees preserve physical destinations. Parent insertion checks every ancestor.
func partitionHashReplayTarget(catalog CatalogRead, d *relationDescriptor) *relationDescriptor {
	target := d
	for child := d; child.Membership != nil; {
		parent := catalog.MustRelation(child.Membership.parent)
		if parent.Partition.strategy == parser.PartitionHash {
			target = parent
		}
		child = parent
	}
	return target
}
