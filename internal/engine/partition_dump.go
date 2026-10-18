package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func partitionDumpPredecessors(view catalog.Read, d *catalog.Relation, predecessors []ids.OID) ([]ids.OID, error) {
	add := func(address ids.ObjectAddress) error {
		id, err := dumpPredecessor(view, address, d.Address())
		if err != nil {
			return err
		}
		if id != 0 && !slices.Contains(predecessors, id) {
			predecessors = append(predecessors, id)
		}
		return nil
	}
	if d.Membership != nil {
		if err := add(d.Membership.Parent); err != nil {
			return nil, err
		}
		if d.Membership.DefaultBound {
			parent := view.MustRelation(d.Membership.Parent)
			for _, address := range parent.Partition.Children {
				if address != d.Address() && !partitionExclusionDefaultBefore(view, d, view.MustRelation(address)) {
					if err := add(address); err != nil {
						return nil, err
					}
				}
			}
		}
		if !d.Membership.DefaultBound {
			for _, tuple := range [2][]catalog.PartitionDatum{d.Membership.Lower, d.Membership.Upper} {
				for _, datum := range tuple {
					if datum.Kind != 0 {
						continue
					}
					literal := literalExpression{Info: expr.Info{Usage: datum.Value.Type().Usage()}, value: datum.Value}
					if err := regclassReplayReferences(view, literal, add); err != nil {
						return nil, err
					}
					var buffer [1]ids.ObjectRef
					references := buffer[:0]
					if err := (scalarRenderer{dependencies: &references}).recordCatalogAliasReferences(view, datum.Value); err != nil {
						return nil, err
					}
					if err := (scalarRenderer{dependencies: &references}).recordRoutineReferences(view, datum.Value); err != nil {
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
		for _, key := range d.Partition.Keys {
			if key.Derived != nil {
				for _, ref := range key.Derived.Dependencies {
					if err := add(ref.Address); err != nil {
						return nil, err
					}
				}
			}
		}
	}
	return predecessors, nil
}

func partitionKeySQL(ctx context.Context, catalog catalog.Read, d *catalog.Relation) (string, error) {
	if d.Partition == nil {
		return "", nil
	}
	var text strings.Builder
	if d.Partition.Strategy == parser.PartitionHash {
		text.WriteString(" PARTITION BY HASH (")
	} else {
		text.WriteString(" PARTITION BY RANGE (")
	}
	for i, key := range d.Partition.Keys {
		if i != 0 {
			text.WriteString(", ")
		}
		if key.Derived != nil {
			expression, err := (scalarRenderer{owner: d}).sql(ctx, catalog, key.Derived.Definition)
			if err != nil {
				return "", err
			}
			text.WriteString("(" + expression + ")")
		} else {
			slot := slices.Index(d.Attributes, key.Attribute)
			if slot < 0 {
				return "", fmt.Errorf("stuntgres: missing partition key attribute")
			}
			text.WriteString(quoteIdentifier(d.Columns[slot].Name))
		}
		if key.Collation != 0 {
			name := collationSQLName(catalog, key.Collation)
			if name == "" {
				return "", fmt.Errorf("stuntgres: unsupported stored collation")
			}
			text.WriteString(" COLLATE " + name)
		}
		if key.ExplicitClass {
			text.WriteString(" " + quoteIdentifier("pg_catalog") + "." + quoteIdentifier(key.Class.Name))
		}
	}
	text.WriteString(")")
	return text.String(), nil
}

// PARTITION OF copies rank zero and inherits order, defaults and NOT NULL names.
// Existing-table attachment need not have any of those properties. Decide from
// captured definitions, not a historical CREATE/ATTACH flag, so logical replay
// remains canonical after stable attribute numbers have been reminted.
func partitionDumpNeedsAttach(catalog catalog.Read, d *catalog.Relation) bool {
	if d.Membership == nil {
		return false
	}
	parent := catalog.MustRelation(d.Membership.Parent)
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

func partitionAttachDumpSQL(ctx context.Context, view catalog.Read, d *catalog.Relation) (string, error) {
	parent := view.MustRelation(d.Membership.Parent)
	qualified := func(table *catalog.Relation) string {
		return quoteIdentifier(view.Get(table.Namespace()).Name()) + "." + quoteIdentifier(table.Name())
	}
	text := "ALTER TABLE " + qualified(parent) + " ATTACH PARTITION " + qualified(d)
	if d.Membership.DefaultBound {
		return text + " DEFAULT;\n", nil
	}
	if d.Membership.Strategy == parser.PartitionHash {
		return text + hashPartitionBoundSQL(d.Membership) + ";\n", nil
	}
	lower, err := partitionTupleDumpSQL(ctx, view, parent.Partition.Keys, d.Membership.Lower)
	if err != nil {
		return "", err
	}
	upper, err := partitionTupleDumpSQL(ctx, view, parent.Partition.Keys, d.Membership.Upper)
	if err != nil {
		return "", err
	}
	return text + " FOR VALUES FROM (" + lower + ") TO (" + upper + ");\n", nil
}

// PARTITION OF names no new column types. Inherited definitions are supplied
// by the parent; only local default/nullability/check differences are emitted.
func partitionChildSQL(ctx context.Context, catalog catalog.Read, d *catalog.Relation, qualified string, deferred map[int32]bool) (string, error) {
	parent, ok := catalog.Relation(d.Membership.Parent)
	if !ok || parent.Partition == nil {
		return "", fmt.Errorf("stuntgres: missing partition dump parent")
	}
	var text strings.Builder
	text.WriteString("CREATE TABLE " + qualified + " PARTITION OF " + quoteIdentifier(catalog.Get(parent.Namespace()).Name()) + "." + quoteIdentifier(parent.Name()))
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
			childSQL, err = defaultSQL(ctx, catalog, childDefault.Expression)
			if err != nil {
				return "", err
			}
		}
		if parentHasDefault {
			parentSQL, err = defaultSQL(ctx, catalog, parentDefault.Expression)
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
				if constraint.Attribute == d.Attributes[i] && (parent.Columns[i].Nullable || constraint.PartitionLocal) {
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
		expression, err := (scalarRenderer{owner: d, checkReplayPrecision: true}).sql(ctx, catalog, constraint.Expression)
		if err != nil {
			return "", err
		}
		inherited := false
		for _, ancestor := range parent.CheckConstraints {
			if ancestor.Name != constraint.Name {
				continue
			}
			parentSQL, err := (scalarRenderer{owner: parent, checkReplayPrecision: true}).sql(ctx, catalog, ancestor.Expression)
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
	if d.Membership.DefaultBound {
		text.WriteString(" DEFAULT")
	} else if d.Membership.Strategy == parser.PartitionHash {
		text.WriteString(hashPartitionBoundSQL(d.Membership))
	} else {
		lower, err := partitionTupleDumpSQL(ctx, catalog, parent.Partition.Keys, d.Membership.Lower)
		if err != nil {
			return "", err
		}
		upper, err := partitionTupleDumpSQL(ctx, catalog, parent.Partition.Keys, d.Membership.Upper)
		if err != nil {
			return "", err
		}
		text.WriteString(" FOR VALUES FROM (" + lower + ") TO (" + upper + ")")
	}
	return text.String(), nil
}

// Bounds are evaluated datums, not saved SQL expressions. The ordinary Dump
// literal authority preserves owned type declarations and reference names.
func partitionTupleDumpSQL(ctx context.Context, catalog catalog.Read, keys []catalog.PartitionKeyEntry, tuple []catalog.PartitionDatum) (string, error) {
	var text strings.Builder
	for i, datum := range tuple {
		if i != 0 {
			text.WriteString(", ")
		}
		switch datum.Kind {
		case -1:
			text.WriteString("MINVALUE")
		case 1:
			text.WriteString("MAXVALUE")
		default:
			literal := literalExpression{Info: expr.Info{Usage: keys[i].Usage}, value: datum.Value}
			value, err := catalogLiteralSQL(ctx, catalog, literal)
			if err != nil {
				return "", err
			}
			text.WriteString(value)
		}
	}
	return text.String(), nil
}

func hashPartitionBoundSQL(bound *catalog.PartitionMembership) string {
	return fmt.Sprintf(" FOR VALUES WITH (MODULUS %d, REMAINDER %d)", bound.Modulus, bound.Remainder)
}

// Replay identity-sensitive rows through their highest HASH ancestor; RANGE-only
// trees preserve physical destinations. Parent insertion checks every ancestor.
func partitionHashReplayTarget(catalog catalog.Read, d *catalog.Relation) *catalog.Relation {
	target := d
	for child := d; child.Membership != nil; {
		parent := catalog.MustRelation(child.Membership.Parent)
		if parent.Partition.Strategy == parser.PartitionHash {
			target = parent
		}
		child = parent
	}
	return target
}
