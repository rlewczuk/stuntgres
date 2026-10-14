package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

func partitionDumpPredecessors(catalog CatalogRead, d *relationDescriptor, predecessors []OID) ([]OID, error) {
	add := func(address ObjectAddress) error {
		id, err := dumpPredecessor(catalog, address, d.address)
		if err != nil {
			return err
		}
		if id != 0 && !slices.Contains(predecessors, id) {
			predecessors = append(predecessors, id)
		}
		return nil
	}
	if d.membership != nil {
		if err := add(d.membership.parent); err != nil {
			return nil, err
		}
		if d.membership.defaultBound {
			parent := catalog.state.descriptors[d.membership.parent].(*relationDescriptor)
			for _, address := range parent.partition.children {
				if address != d.address {
					if err := add(address); err != nil {
						return nil, err
					}
				}
			}
		}
		if !d.membership.defaultBound {
			for _, tuple := range [2][]partitionDatum{d.membership.lower, d.membership.upper} {
				for _, datum := range tuple {
					if datum.kind != 0 {
						continue
					}
					literal := literalExpression{expressionInfo: expressionInfo{usage: datum.value.Type().Usage()}, value: datum.value}
					if err := regclassReplayReferences(catalog, literal, add); err != nil {
						return nil, err
					}
					var buffer [1]ObjectRef
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
	if d.partition != nil {
		for _, key := range d.partition.keys {
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
	if d.partition == nil {
		return "", nil
	}
	var text strings.Builder
	text.WriteString(" PARTITION BY RANGE (")
	for i, key := range d.partition.keys {
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
			slot := slices.Index(d.attributes, key.attribute)
			if slot < 0 {
				return "", fmt.Errorf("stuntgres: missing partition key attribute")
			}
			text.WriteString(quoteIdentifier(d.columns[slot].Name))
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
	if d.membership == nil {
		return false
	}
	parent := catalog.state.descriptors[d.membership.parent].(*relationDescriptor)
	for i, column := range d.columns {
		if column.DeclaredDimensions != 0 || column.Name != parent.columns[i].Name {
			return true
		}
		attribute, parentAttribute := d.attributes[i], parent.attributes[i]
		if _, exists := parent.defaults[parentAttribute]; exists {
			if _, exists := d.defaults[attribute]; !exists {
				return true
			}
		}
		for _, child := range d.notNullConstraints {
			if child.Attribute != attribute {
				continue
			}
			if child.NotValid {
				return true
			}
			for _, inherited := range parent.notNullConstraints {
				if inherited.Attribute == parentAttribute && child.Name != inherited.Name {
					return true
				}
			}
		}
	}
	for _, check := range d.checkConstraints {
		if check.NotValid || check.NoInherit {
			return true
		}
	}
	return false
}

func partitionAttachDumpSQL(ctx context.Context, catalog CatalogRead, d *relationDescriptor) (string, error) {
	parent := catalog.state.descriptors[d.membership.parent].(*relationDescriptor)
	qualified := func(table *relationDescriptor) string {
		return quoteIdentifier(catalog.state.descriptors[table.namespace].Name()) + "." + quoteIdentifier(table.name)
	}
	text := "ALTER TABLE " + qualified(parent) + " ATTACH PARTITION " + qualified(d)
	if d.membership.defaultBound {
		return text + " DEFAULT;\n", nil
	}
	lower, err := partitionTupleDumpSQL(ctx, catalog, parent.partition.keys, d.membership.lower)
	if err != nil {
		return "", err
	}
	upper, err := partitionTupleDumpSQL(ctx, catalog, parent.partition.keys, d.membership.upper)
	if err != nil {
		return "", err
	}
	return text + " FOR VALUES FROM (" + lower + ") TO (" + upper + ");\n", nil
}

// PARTITION OF names no new column types. Inherited definitions are supplied
// by the parent; only local default/nullability/check differences are emitted.
func partitionChildSQL(ctx context.Context, catalog CatalogRead, d *relationDescriptor, qualified string, deferred map[int32]bool) (string, error) {
	parent, ok := catalog.state.descriptors[d.membership.parent].(*relationDescriptor)
	if !ok || parent.partition == nil {
		return "", fmt.Errorf("stuntgres: missing partition dump parent")
	}
	var text strings.Builder
	text.WriteString("CREATE TABLE " + qualified + " PARTITION OF " + quoteIdentifier(catalog.state.descriptors[parent.namespace].Name()) + "." + quoteIdentifier(parent.name))
	var options strings.Builder
	appendOption := func(option string) {
		if options.Len() != 0 {
			options.WriteString(", ")
		}
		options.WriteString(option)
	}
	for i, column := range d.columns {
		var option strings.Builder
		option.WriteString(quoteIdentifier(column.Name))
		changed := false
		childDefault, childHasDefault := d.defaults[d.attributes[i]]
		parentDefault, parentHasDefault := parent.defaults[parent.attributes[i]]
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
		if !deferred[d.attributes[i]] && (childHasDefault != parentHasDefault || childSQL != parentSQL) {
			option.WriteString(" DEFAULT " + childSQL)
			changed = true
		}
		if !column.Nullable {
			for _, constraint := range d.notNullConstraints {
				if constraint.Attribute == d.attributes[i] && (parent.columns[i].Nullable || constraint.partitionLocal) {
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
		for _, ancestor := range parent.checkConstraints {
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
	for _, constraint := range d.uniqueConstraints {
		if constraint.Parent != 0 {
			continue
		}
		index := catalog.state.descriptors[constraint.Index].(*relationDescriptor)
		if partitionReplaySeparateConstraint(catalog, index) {
			continue
		}
		definition, err := (pgRenderer{view: catalog}).uniqueConstraintSQL(d, &constraint)
		if err != nil {
			return "", err
		}
		appendOption("CONSTRAINT " + quoteIdentifier(constraint.Name) + " " + definition)
	}
	for _, constraint := range d.exclusionConstraints {
		index, ok := catalog.state.descriptors[constraint.Index].(*relationDescriptor)
		if !ok {
			return "", fmt.Errorf("stuntgres: missing exclusion dump index")
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
	if d.membership.defaultBound {
		text.WriteString(" DEFAULT")
	} else {
		lower, err := partitionTupleDumpSQL(ctx, catalog, parent.partition.keys, d.membership.lower)
		if err != nil {
			return "", err
		}
		upper, err := partitionTupleDumpSQL(ctx, catalog, parent.partition.keys, d.membership.upper)
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
			literal := literalExpression{expressionInfo: expressionInfo{usage: keys[i].usage}, value: datum.value}
			value, err := catalogLiteralSQL(ctx, catalog, literal)
			if err != nil {
				return "", err
			}
			text.WriteString(value)
		}
	}
	return text.String(), nil
}
