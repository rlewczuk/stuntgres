package engine

import (
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/parser"
)

const RestorePhysicalNotNullBoundary = "restore accepts only paired canonical physical not-null cleanup"

type physicalNotNullRepair struct {
	Relation     parser.ObjectName
	RelationText string
	Column       string
	Primary      string
	Keys         []string
}

func physicalNotNullCleanupSQL(relation, column, primary string, keys []string) string {
	attribute := func(name string) string {
		return "(SELECT attnum FROM pg_catalog.pg_attribute WHERE attrelid = " + dump.CommentLiteral(relation) + "::regclass AND attname = " + dump.CommentLiteral(name) + ")"
	}
	keySQL := make([]string, len(keys))
	for i, name := range keys {
		keySQL[i] = attribute(name)
	}
	guard := "NOT EXISTS (SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid = " + dump.CommentLiteral(relation) + "::regclass AND contype = 'p')"
	if primary != "" {
		guard = "EXISTS (SELECT 1 FROM pg_catalog.pg_constraint WHERE conrelid = " + dump.CommentLiteral(relation) + "::regclass AND conname = " + dump.CommentLiteral(primary) + " AND contype = 'p' AND conkey = ARRAY[" + strings.Join(keySQL, ", ") + "])"
	}
	return fmt.Sprintf("WITH target AS (SELECT nn.oid FROM pg_catalog.pg_constraint AS nn WHERE nn.conrelid = %s::regclass AND nn.contype = 'n' AND nn.conkey = ARRAY[%s] AND nn.conislocal = true AND nn.coninhcount = 0 AND nn.conparentid = 0 AND nn.convalidated = true AND nn.connoinherit = false AND %s AND NOT EXISTS (SELECT 1 FROM pg_catalog.pg_depend WHERE refclassid = 'pg_catalog.pg_constraint'::regclass AND refobjid = nn.oid)), removed_dependencies AS (DELETE FROM pg_catalog.pg_depend WHERE classid = 'pg_catalog.pg_constraint'::regclass AND objid IN (SELECT oid FROM target) RETURNING objid) DELETE FROM pg_catalog.pg_constraint WHERE oid IN (SELECT oid FROM target)", dump.CommentLiteral(relation), attribute(column), guard)
}

func physicalColumn(raw parser.Expr, parts ...string) bool {
	column, ok := raw.(parser.ColumnExpr)
	if !ok || len(column.Name.Parts) != len(parts) {
		return false
	}
	for i, part := range parts {
		if column.Name.Parts[i].Text != part {
			return false
		}
	}
	return true
}
func physicalEquality(raw parser.Expr, parts ...string) (parser.Expr, bool) {
	operator, ok := raw.(parser.OperatorExpr)
	if !ok || len(operator.Name.Parts) != 1 || operator.Name.Parts[0].Text != "=" || len(operator.Args) != 2 || !physicalColumn(operator.Args[0], parts...) {
		return nil, false
	}
	return operator.Args[1], true
}
func physicalTerms(raw parser.Expr, count int) ([]parser.Expr, bool) {
	var terms []parser.Expr
	retiredTerms(raw, &terms)
	return terms, len(terms) == count
}
func physicalTextEquality(raw parser.Expr, text string, parts ...string) bool {
	value, ok := physicalEquality(raw, parts...)
	actual, literal := retiredText(value)
	return ok && literal && actual == text
}
func physicalIntegerEquality(raw parser.Expr, expected int64, parts ...string) bool {
	value, ok := physicalEquality(raw, parts...)
	actual, literal := retiredInteger(value)
	return ok && literal && actual == expected
}
func physicalBooleanEquality(raw parser.Expr, expected bool, parts ...string) bool {
	value, ok := physicalEquality(raw, parts...)
	actual, literal := retiredBoolean(value)
	return ok && literal && actual == expected
}
func physicalFrom(query *parser.Select, name string) bool {
	if !restorePlainSelect(query) || query.Values != nil {
		return false
	}
	table, ok := query.From.(*parser.TableReference)
	return ok && !table.Only && RestoreCatalogName(table.Name, name)
}
func physicalOne(query *parser.Select) bool {
	if len(query.Targets) != 1 {
		return false
	}
	value, ok := retiredInteger(query.Targets[0])
	return ok && value == 1
}
func physicalExists(raw parser.Expr, negative bool) (*parser.Select, bool) {
	if negative {
		not, ok := raw.(parser.LogicalExpr)
		if !ok || not.Op != parser.LogicalNot || not.Right != nil {
			return nil, false
		}
		raw = not.Left
	}
	exists, ok := raw.(parser.SubqueryExpr)
	return exists.Query, ok && exists.Kind == parser.SubqueryExists && !exists.Negate && exists.Left == nil && len(exists.Operator.Parts) == 0 && exists.Query != nil
}
func physicalRegclassEquality(raw parser.Expr, text string, parts ...string) bool {
	value, ok := physicalEquality(raw, parts...)
	_, actual, regclass := restoreNoInheritRegclass(value)
	return ok && regclass && actual == text
}
func physicalArray(raw parser.Expr, relation string) ([]string, bool) {
	array, ok := raw.(parser.ArrayExpr)
	if !ok || len(array.Elements) == 0 {
		return nil, false
	}
	columns := make([]string, len(array.Elements))
	for i, value := range array.Elements {
		column, valid := restoreConstraintAttribute(value, relation)
		if !valid || slices.Contains(columns[:i], column) {
			return nil, false
		}
		columns[i] = column
	}
	return columns, true
}
func physicalTargetMembership(raw parser.Expr, column string) bool {
	member, ok := raw.(parser.SubqueryExpr)
	if !ok || member.Kind != parser.SubqueryAny || member.Negate || len(member.Operator.Parts) != 1 || member.Operator.Parts[0].Text != "=" || !physicalColumn(member.Left, column) {
		return false
	}
	query := member.Query
	if !restorePlainSelect(query) || query.Values != nil || query.Where != nil || len(query.Targets) != 1 || !physicalColumn(query.Targets[0], "oid") {
		return false
	}
	table, ok := query.From.(*parser.TableReference)
	return ok && !table.Only && len(table.Name.Parts) == 1 && table.Name.Parts[0].Text == "target"
}
func physicalDelete(raw *parser.Delete, name string) bool {
	return raw != nil && RestoreCatalogName(raw.Name, name) && !raw.Only && !raw.Star && raw.Alias.Text == "" && raw.Using == nil && raw.CurrentOf == nil && !raw.WithRecursive
}

// Recognize only the closed, data-modifying CTE emitted above. Its dependency
// deletion removes the selected NN's outgoing edges, never incoming objects.
func RestorePhysicalNotNullSyntax(raw *parser.Delete) (physicalNotNullRepair, bool) {
	var repair physicalNotNullRepair
	if !physicalDelete(raw, "pg_constraint") || len(raw.With) != 2 || raw.Returning != nil || !physicalTargetMembership(raw.Where, "oid") {
		return repair, false
	}
	for i, name := range []string{"target", "removed_dependencies"} {
		cte := raw.With[i]
		if cte.Name.Text != name || len(cte.Columns) != 0 || cte.Materialization != 0 {
			return repair, false
		}
	}
	dependencies, ok := raw.With[1].Query.(*parser.Delete)
	if !ok || !physicalDelete(dependencies, "pg_depend") || len(dependencies.With) != 0 || dependencies.Returning == nil || len(dependencies.Returning.Targets) != 1 || len(dependencies.Returning.Aliases) != 0 || !physicalColumn(dependencies.Returning.Targets[0], "objid") {
		return repair, false
	}
	for _, label := range dependencies.Returning.Labels {
		if label.Text != "" {
			return repair, false
		}
	}
	terms, ok := physicalTerms(dependencies.Where, 2)
	if !ok || !physicalRegclassEquality(terms[0], "pg_catalog.pg_constraint", "classid") || !physicalTargetMembership(terms[1], "objid") {
		return repair, false
	}
	target, ok := raw.With[0].Query.(*parser.Select)
	if !ok || !restorePlainSelect(target) || target.Values != nil || len(target.Targets) != 1 || !physicalColumn(target.Targets[0], "nn", "oid") {
		return repair, false
	}
	alias, ok := target.From.(*parser.AliasReference)
	if !ok || alias.Name.Text != "nn" || len(alias.Columns) != 0 || len(alias.ColumnDefinitions) != 0 {
		return repair, false
	}
	table, ok := alias.Input.(*parser.TableReference)
	if !ok || table.Only || !RestoreCatalogName(table.Name, "pg_constraint") {
		return repair, false
	}
	terms, ok = physicalTerms(target.Where, 10)
	if !ok {
		return repair, false
	}
	value, ok := physicalEquality(terms[0], "nn", "conrelid")
	if !ok {
		return repair, false
	}
	repair.Relation, repair.RelationText, ok = restoreNoInheritRegclass(value)
	if !ok || len(repair.Relation.Parts) != 2 || !physicalTextEquality(terms[1], "n", "nn", "contype") {
		return repair, false
	}
	value, ok = physicalEquality(terms[2], "nn", "conkey")
	columns, valid := physicalArray(value, repair.RelationText)
	if !ok || !valid || len(columns) != 1 {
		return repair, false
	}
	repair.Column = columns[0]
	if !physicalBooleanEquality(terms[3], true, "nn", "conislocal") || !physicalIntegerEquality(terms[4], 0, "nn", "coninhcount") || !physicalIntegerEquality(terms[5], 0, "nn", "conparentid") || !physicalBooleanEquality(terms[6], true, "nn", "convalidated") || !physicalBooleanEquality(terms[7], false, "nn", "connoinherit") {
		return repair, false
	}
	incoming, ok := physicalExists(terms[9], true)
	if !ok || !physicalFrom(incoming, "pg_depend") || !physicalOne(incoming) {
		return repair, false
	}
	incomingTerms, ok := physicalTerms(incoming.Where, 2)
	if !ok || !physicalRegclassEquality(incomingTerms[0], "pg_catalog.pg_constraint", "refclassid") {
		return repair, false
	}
	value, ok = physicalEquality(incomingTerms[1], "refobjid")
	if !ok || !physicalColumn(value, "nn", "oid") {
		return repair, false
	}
	primary, positive := physicalExists(terms[8], false)
	if !positive {
		primary, ok = physicalExists(terms[8], true)
	} else {
		ok = true
	}
	if !ok || !physicalFrom(primary, "pg_constraint") || !physicalOne(primary) {
		return repair, false
	}
	count := 2
	if positive {
		count = 4
	}
	primaryTerms, ok := physicalTerms(primary.Where, count)
	if !ok || !physicalRegclassEquality(primaryTerms[0], repair.RelationText, "conrelid") {
		return repair, false
	}
	if !positive {
		return repair, physicalTextEquality(primaryTerms[1], "p", "contype")
	}
	value, ok = physicalEquality(primaryTerms[1], "conname")
	repair.Primary, valid = retiredText(value)
	if !ok || !valid || repair.Primary == "" || !physicalTextEquality(primaryTerms[2], "p", "contype") {
		return repair, false
	}
	value, ok = physicalEquality(primaryTerms[3], "conkey")
	repair.Keys, valid = physicalArray(value, repair.RelationText)
	return repair, ok && valid && slices.Contains(repair.Keys, repair.Column)
}

func SamePhysicalRepair(a, b physicalNotNullRepair) bool {
	return a.RelationText == b.RelationText && a.Column == b.Column && a.Primary == b.Primary && slices.Equal(a.Keys, b.Keys)
}
