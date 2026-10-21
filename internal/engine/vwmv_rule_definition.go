package engine

import (
	"fmt"
	"slices"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

func ruleIndex(d *catalog.Relation, name string) int {
	return slices.IndexFunc(stored.RulesOf(d), func(r stored.Rule) bool { return r.Name == name })
}

func ruleMissing(name string, d *catalog.Relation) error {
	return sqlError("42704", fmt.Sprintf(`rule "%s" for relation "%s" does not exist`, name, d.Name()))
}

func ruleDuplicate(name string, d *catalog.Relation) error {
	return sqlError("42710", fmt.Sprintf(`rule "%s" for relation "%s" already exists`, name, d.Name()))
}

func ruleResultCheck(d *catalog.Relation, schema []catalog.Column, selectRule bool) error {
	prefix := "RETURNING list"
	if selectRule {
		prefix = "SELECT rule's target list"
	}
	if d.ColumnMeta.HighWater > int32(len(d.Columns)) {
		message := "cannot create a RETURNING list for a relation containing dropped columns"
		if selectRule {
			message = "cannot convert relation containing dropped columns to view"
		}
		return sqlError("0A000", message)
	}
	if len(schema) < len(d.Columns) {
		return sqlError("42P17", prefix+" has too few entries")
	}
	if len(schema) > len(d.Columns) {
		return sqlError("42P17", prefix+" has too many entries")
	}
	for i, column := range schema {
		want := d.Columns[i]
		if selectRule && column.Name != want.Name {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: fmt.Sprintf(`SELECT rule's target entry %d has different column name from column "%s"`, i+1, want.Name), Detail: fmt.Sprintf(`SELECT target entry is named "%s".`, column.Name)}
		}
		if column.Usage.Type != want.Usage.Type {
			prefix = "RETURNING list's"
			if selectRule {
				prefix = "SELECT rule's target"
			}
			detail := "RETURNING list entry"
			if selectRule {
				detail = "SELECT target entry"
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: fmt.Sprintf(`%s entry %d has different type from column "%s"`, prefix, i+1, want.Name), Detail: fmt.Sprintf("%s has type %s, but column has type %s.", detail, column.Usage.Type.DiagnosticName(), want.Usage.Type.DiagnosticName())}
		}
		if want.Usage.Typmod != -1 && column.Usage.Typmod != -1 && column.Usage.Typmod != want.Usage.Typmod {
			prefix = "RETURNING list's"
			detail := "RETURNING list entry"
			if selectRule {
				prefix = "SELECT rule's target"
				detail = "SELECT target entry"
			}
			actual, err := analyze.PGType(column.Usage)
			if err != nil {
				return err
			}
			expected, err := analyze.PGType(want.Usage)
			if err != nil {
				return err
			}
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P17", Message: fmt.Sprintf(`%s entry %d has different size from column "%s"`, prefix, i+1, want.Name), Detail: fmt.Sprintf("%s has type %s, but column has type %s.", detail, actual, expected)}
		}
	}
	return nil
}

func ruleReturning(s parser.Statement) *parser.Returning {
	switch s := s.(type) {
	case *parser.Insert:
		return s.Returning
	case *parser.Update:
		return s.Returning
	case *parser.Delete:
		return s.Returning
	}
	return nil
}
