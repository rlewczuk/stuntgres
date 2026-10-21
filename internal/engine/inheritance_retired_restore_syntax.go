package engine

import (
	"slices"
	"strconv"
	"strings"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

func restorePlainSelect(q *parser.Select) bool {
	if q == nil || len(q.With) != 0 || q.WithRecursive || q.GroupBy != nil || q.Having != nil || len(q.Windows) != 0 || q.Order != nil || q.Into != nil || len(q.Locking) != 0 || q.Bounds != nil || q.Distinct != nil || q.Set != nil {
		return false
	}
	for _, label := range q.Labels {
		if label.Text != "" {
			return false
		}
	}
	return true
}
func retiredText(raw parser.Expr) (string, bool) {
	v, ok := raw.(parser.LiteralExpr)
	return v.Value.Text, ok && v.Value.Kind == parser.StringValue
}
func retiredInteger(raw parser.Expr) (int64, bool) {
	v, ok := raw.(parser.LiteralExpr)
	return v.Value.Integer, ok && v.Value.Kind == parser.IntegerValue
}
func retiredBoolean(raw parser.Expr) (bool, bool) {
	v, ok := raw.(parser.LiteralExpr)
	return v.Value.Boolean, ok && v.Value.Kind == parser.BooleanValue
}
func retiredTerms(raw parser.Expr, terms *[]parser.Expr) {
	if logical, ok := raw.(parser.LogicalExpr); ok && logical.Op == parser.LogicalAnd {
		retiredTerms(logical.Left, terms)
		retiredTerms(logical.Right, terms)
	} else {
		*terms = append(*terms, raw)
	}
}

func RestoreRetiredSyntax(raw *parser.Update) (retiredRepair, error) {
	var result retiredRepair
	reject := func() (retiredRepair, error) { return retiredRepair{}, sqlError("0A000", RestoreRetiredBoundary) }
	if !RestoreCatalogName(raw.Name, "pg_constraint") || raw.Only || raw.Star || raw.WithRecursive || len(raw.With) != 0 || raw.Alias.Text != "" || raw.From != nil || raw.CurrentOf != nil || raw.Returning != nil || len(raw.Assignments) != 2 {
		return reject()
	}
	for _, a := range raw.Assignments {
		if len(a.Steps) != 0 {
			return reject()
		}
	}
	if raw.Assignments[0].Target.Text != "conislocal" || raw.Assignments[1].Target.Text != "coninhcount" {
		return reject()
	}
	local, ok := retiredBoolean(raw.Assignments[0].Value)
	if !ok {
		return reject()
	}
	result.Local = local
	sum, ok := raw.Assignments[1].Value.(parser.OperatorExpr)
	if !ok || len(sum.Name.Parts) != 1 || sum.Name.Parts[0].Text != "+" || len(sum.Args) != 2 {
		return reject()
	}
	before, ok := retiredInteger(sum.Args[0])
	if !ok || before < 0 || before > 32767 {
		return reject()
	}
	result.Before = int(before)
	sub, ok := sum.Args[1].(parser.SubqueryExpr)
	if !ok || sub.Kind != parser.SubqueryScalar || sub.Left != nil || sub.Negate || len(sub.Operator.Parts) != 0 || !restorePlainSelect(sub.Query) || sub.Query.Values != nil || sub.Query.Where != nil || len(sub.Query.Targets) != 1 {
		return reject()
	}
	// The ordinary COUNT grammar produces AggregateExpr, not FunctionExpr.
	// Accept only the emitted unqualified DISTINCT aggregate and fixed row key.
	count, ok := sub.Query.Targets[0].(parser.AggregateExpr)
	if !ok || count.Name != "count" || count.Schema != nil || !count.Distinct || count.Star || count.Variadic || count.Window != nil || count.Filter != nil || count.FilterPos != (parser.Position{}) || len(count.Order) != 0 || count.Within || count.WithinPos != (parser.Position{}) || count.JSON != nil || len(count.Args) != 1 {
		return reject()
	}
	identity, ok := count.Args[0].(parser.RowExpr)
	if !ok || identity.Explicit || len(identity.Fields) != 3 {
		return reject()
	}
	for i, name := range []string{"column1", "column6", "column7"} {
		column, ok := identity.Fields[i].(parser.ColumnExpr)
		if !ok || len(column.Name.Parts) != 2 || column.Name.Parts[0].Text != "retired" || column.Name.Parts[1].Text != name {
			return reject()
		}
	}
	alias, ok := sub.Query.From.(*parser.AliasReference)
	if !ok || alias.Name.Text != "retired" || len(alias.Columns) != 0 || len(alias.ColumnDefinitions) != 0 {
		return reject()
	}
	var values *parser.ValuesQuery
	switch input := alias.Input.(type) {
	case *parser.ValuesQuery:
		values = input
	case *parser.DerivedQuery:
		if !restorePlainSelect(input.Query) || input.Query.From != nil || len(input.Query.Targets) != 0 || input.Query.Where != nil {
			return reject()
		}
		values = input.Query.Values
	}
	if values == nil || values.Order != nil || values.Bounds != nil || len(values.Rows) == 0 || len(values.Rows)+result.Before > 32767 {
		return reject()
	}
	for _, row := range values.Rows {
		if len(row) != 7 {
			return reject()
		}
		parent, text, ok := restoreNoInheritRegclass(row[0])
		if !ok {
			return reject()
		}
		oldColumn, ok := retiredText(row[1])
		if !ok || oldColumn == "" {
			return reject()
		}
		attribute, ok := retiredInteger(row[2])
		if !ok || attribute <= 0 || attribute > 1600 {
			return reject()
		}
		kind, ok := retiredText(row[3])
		if !ok || (kind != "n" && kind != "c") {
			return reject()
		}
		name, ok := retiredText(row[4])
		if !ok || name == "" {
			return reject()
		}
		lifetimeText, ok := retiredText(row[5])
		if !ok {
			return reject()
		}
		parts := strings.Split(lifetimeText, ":")
		if len(parts) != 2 {
			return reject()
		}
		archive, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil || strconv.FormatUint(archive, 10) != parts[0] {
			return reject()
		}
		lifetime, err := strconv.ParseUint(parts[1], 10, 64)
		if err != nil || lifetime == 0 || strconv.FormatUint(lifetime, 10) != parts[1] {
			return reject()
		}
		oidText, ok := retiredText(row[6])
		if !ok {
			return reject()
		}
		oid, err := strconv.ParseUint(oidText, 10, 32)
		if err != nil || oid == 0 || strconv.FormatUint(oid, 10) != oidText {
			return reject()
		}
		result.Rows = append(result.Rows, retiredRepairRow{Parent: parent, text: text, Column: oldColumn, Attribute: int32(attribute), Check: kind == "c", Name: name, Lifetime: lifetime, Archive: archive, Constraint: ids.OID(oid)})
		if !slices.Contains(result.parents, text) {
			result.parents = append(result.parents, text)
		}
	}
	var terms []parser.Expr
	retiredTerms(raw.Where, &terms)
	if len(terms) != 6+len(result.parents) {
		return reject()
	}
	relation, ok := restoreAttributeRankEquality(terms[0], "conrelid")
	if !ok {
		return reject()
	}
	result.Relation, result.text, ok = restoreNoInheritRegclass(relation)
	if !ok {
		return reject()
	}
	name, ok := restoreAttributeRankEquality(terms[1], "conname")
	if !ok {
		return reject()
	}
	result.Name, ok = retiredText(name)
	if !ok || result.Name == "" {
		return reject()
	}
	kindRaw, ok := restoreAttributeRankEquality(terms[2], "contype")
	if !ok {
		return reject()
	}
	kind, ok := retiredText(kindRaw)
	if !ok || (kind != "n" && kind != "c") {
		return reject()
	}
	result.Check = kind == "c"
	key, ok := restoreAttributeRankEquality(terms[3], "conkey")
	if !ok {
		return reject()
	}
	array, ok := key.(parser.ArrayExpr)
	if !ok || len(array.Elements) == 0 {
		return reject()
	}
	for _, element := range array.Elements {
		column, ok := restoreConstraintAttribute(element, result.text)
		if !ok || slices.Contains(result.Columns, column) {
			return reject()
		}
		result.Columns = append(result.Columns, column)
	}
	localRaw, ok := restoreAttributeRankEquality(terms[4], "conislocal")
	if !ok {
		return reject()
	}
	result.BeforeLocal, ok = retiredBoolean(localRaw)
	if !ok {
		return reject()
	}
	countRaw, ok := restoreAttributeRankEquality(terms[5], "coninhcount")
	if !ok {
		return reject()
	}
	expected, ok := retiredInteger(countRaw)
	if !ok || expected != int64(result.Before) {
		return reject()
	}
	for i, parent := range result.parents {
		exists, ok := terms[6+i].(parser.SubqueryExpr)
		if !ok || exists.Kind != parser.SubqueryExists || exists.Negate || exists.Left != nil || len(exists.Operator.Parts) != 0 || !restorePlainSelect(exists.Query) || exists.Query.Values != nil || len(exists.Query.Targets) != 1 {
			return reject()
		}
		one, ok := retiredInteger(exists.Query.Targets[0])
		if !ok || one != 1 {
			return reject()
		}
		from, ok := exists.Query.From.(*parser.TableReference)
		if !ok || from.Only || !RestoreCatalogName(from.Name, "pg_inherits") {
			return reject()
		}
		predicate, ok := exists.Query.Where.(parser.LogicalExpr)
		if !ok || predicate.Op != parser.LogicalAnd {
			return reject()
		}
		child, ok := restoreAttributeRankEquality(predicate.Left, "inhrelid")
		if !ok {
			return reject()
		}
		_, childText, ok := restoreNoInheritRegclass(child)
		if !ok || childText != result.text {
			return reject()
		}
		source, ok := restoreAttributeRankEquality(predicate.Right, "inhparent")
		if !ok {
			return reject()
		}
		_, sourceText, ok := restoreNoInheritRegclass(source)
		if !ok || sourceText != parent {
			return reject()
		}
	}
	for _, row := range result.Rows {
		if row.Check != result.Check {
			return reject()
		}
	}
	return result, nil
}
