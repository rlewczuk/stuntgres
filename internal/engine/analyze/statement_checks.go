package analyze

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/pgcatalog"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func BindViewOutput(query *plan.Query, view *parser.CreateView) error {
	columns := query.Root.Schema()
	if len(view.Columns) > len(columns) {
		return sqlError("42601", "CREATE VIEW specifies more column names than columns")
	}
	for i := range columns {
		if i < len(view.Columns) {
			columns[i].Name = view.Columns[i].Text
		}
		columns[i].Nullable, columns[i].NullabilityKnown = true, true
	}
	if !view.Replace {
		return CheckViewOutputTypes(columns)
	}
	return nil
}

func CheckViewOutputTypes(columns []catalog.Column) error {
	for _, column := range columns {
		if column.Usage.Type.ID() == sqltype.Void {
			return sqlError("42P16", fmt.Sprintf(`column "%s" has pseudo-type void`, column.Name))
		}
	}
	return nil
}

// slots may be nil for the target-first lock-analysis checkpoint. Repeated
// partial targets compose; any repeated whole-column target remains an error.
func BindInsertTargets(relation *catalog.Relation, targets []parser.InsertTarget, slots []int) error {
	for i, target := range targets {
		slot, ok := relation.Index[target.Name]
		if !ok {
			return insertError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, target.Name, relation.Name()), target.Pos)
		}
		for _, previous := range targets[:i] {
			if previous.Name == target.Name && (len(previous.Steps) == 0 || len(target.Steps) == 0) {
				return insertError("42701", fmt.Sprintf(`column "%s" specified more than once`, target.Name), target.Pos)
			}
		}
		if slots != nil {
			slots[i] = slot
		}
	}
	return nil
}

func InsertValueError(view catalog.Read, err error, column catalog.Column, raw parser.Expr, sourceType *sqltype.Descriptor) error {
	sqlErr, ok := err.(*sqlstate.Error)
	if !ok {
		return err
	}
	copy := *sqlErr
	value := parser.Value{Pos: raw.SourcePosition()}
	if literal, ok := raw.(parser.LiteralExpr); ok {
		value = literal.Value
	}
	switch copy.Code {
	case "42804":
		if strings.HasPrefix(copy.Message, "column \"") {
			return &copy
		}
		var source string
		if sourceType != nil {
			source = sourceType.DiagnosticName()
		} else {
			source = LiteralTypeName(value)
			if literal, ok := raw.(parser.TypedLiteralExpr); ok {
				found := view.Lookup(ids.NameKey{ClassID: ids.TypeClassID, Namespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, Name: literal.TypeName.Name})
				if len(found) == 1 {
					if typ, ok := view.LookupType(sqltype.ID(found[0].ObjectID)); ok {
						source = typ.DiagnosticName()
					}
				}
			}
		}
		copy.Message = fmt.Sprintf("column \"%s\" is of type %s but expression is of type %s", column.Name, column.Usage.Type.DiagnosticName(), source)
		copy.Hint = "You will need to rewrite or cast the expression."
		copy.Position = strconv.Itoa(value.Pos.Character)
	}
	return &copy
}

func LiteralTypeName(raw parser.Value) string {
	switch raw.Kind {
	case parser.BitStringValue:
		return "bit"
	case parser.NumericValue:
		return "numeric"
	case parser.IntegerValue:
		if raw.Integer < -2147483648 || raw.Integer > 2147483647 {
			return "bigint"
		}
		return "integer"
	case parser.BooleanValue:
		return "boolean"
	}
	return "unknown"
}

func GenerationWriteError(column string, update bool) error {
	message := fmt.Sprintf(`cannot insert a non-DEFAULT value into column "%s"`, column)
	if update {
		message = fmt.Sprintf(`column "%s" can only be updated to DEFAULT`, column)
	}
	diagnostic := sqlError("428C9", message).(*sqlstate.Error)
	diagnostic.Detail = fmt.Sprintf(`Column "%s" is a generated column.`, column)
	return diagnostic
}

func GenerationInheritanceError(column string, parent, child catalog.GenerationKind) error {
	err := sqlError("42611", fmt.Sprintf(`column "%s" inherits from generated column of different kind`, column)).(*sqlstate.Error)
	err.Detail = fmt.Sprintf("Parent column is %s, child column is %s.", parent.SQL(), child.SQL())
	return err
}

func inheritanceTypeName(u sqltype.Usage) string {
	name := u.Type.DiagnosticName()
	if u.Typmod != u.Type.Usage().Typmod {
		declaration, _ := u.Declaration()
		name += strings.TrimPrefix(declaration, u.Type.SQLName())
	}
	return name
}

func InheritanceColumnConflict(view catalog.Read, old, next catalog.Column, local bool) error {
	prefix := "inherited column"
	if local {
		prefix = "column"
	}
	if old.Usage.Type != next.Usage.Type || old.Usage.Typmod != next.Usage.Typmod {
		d := sqlError("42804", fmt.Sprintf(`%s "%s" has a type conflict`, prefix, old.Name)).(*sqlstate.Error)
		d.Detail = inheritanceTypeName(old.Usage) + " versus " + inheritanceTypeName(next.Usage)
		return d
	}
	if old.Usage.Collation.ID != next.Usage.Collation.ID {
		d := sqlError("42P21", fmt.Sprintf(`%s "%s" has a collation conflict`, prefix, old.Name)).(*sqlstate.Error)
		d.Detail = fmt.Sprintf(`"%s" versus "%s"`, typedef.CollationName(view, old.Usage.Collation.ID), typedef.CollationName(view, next.Usage.Collation.ID))
		return d
	}
	return nil
}

func TableKindError(d *catalog.Relation, operation string) error {
	if d.RelationKind == catalog.TableRelation || d.RelationKind == catalog.PartitionedRelation {
		return nil
	}
	if d.RelationKind == catalog.CompositeRelation {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot %s relation "%s"`, operation, d.Name()), Detail: "This operation is not supported for composite types."}
	}
	if d.RelationKind == catalog.MaterializedViewRelation && operation == "change" {
		return sqlError("42809", fmt.Sprintf(`cannot change materialized view "%s"`, d.Name()))
	}
	if operation == "ADD COLUMN" {
		kind := ""
		switch d.RelationKind {
		case catalog.ViewRelation:
			kind = "views"
		case catalog.MaterializedViewRelation:
			kind = "materialized views"
		case catalog.IndexRelation:
			kind = "indexes"
		case catalog.SequenceRelation:
			kind = "sequences"
		}
		if kind != "" {
			return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`ALTER action ADD COLUMN cannot be performed on relation "%s"`, d.Name()), Detail: "This operation is not supported for " + kind + "."}
		}
	}
	if d.RelationKind == catalog.MaterializedViewRelation {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot %s relation "%s"`, operation, d.Name()), Detail: "This operation is not supported for materialized views."}
	}
	if d.RelationKind == catalog.IndexRelation {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot %s relation "%s"`, operation, d.Name()), Detail: "This operation is not supported for indexes."}
	}
	if d.RelationKind == catalog.ViewRelation {
		return sqlError("0A000", "view mutation is unsupported")
	}
	if d.RelationKind == catalog.CatalogRelation {
		return sqlError("0A000", "catalog source mutation is unsupported")
	}
	message := fmt.Sprintf(`cannot change sequence "%s"`, d.Name())
	detail := ""
	switch operation {
	case "like":
		message = fmt.Sprintf(`relation "%s" is invalid in LIKE clause`, d.Name())
		detail = "This operation is not supported for sequences."
	case "comment table":
		message = fmt.Sprintf(`"%s" is not a table`, d.Name())
	case "comment column":
		message = fmt.Sprintf(`cannot set comment on relation "%s"`, d.Name())
		detail = "This operation is not supported for sequences."
	case "lock":
		message = fmt.Sprintf(`cannot lock relation "%s"`, d.Name())
		detail = "This operation is not supported for sequences."
	default:
		if operation != "insert into" && operation != "delete from" && operation != "change" {
			message = fmt.Sprintf(`ALTER action %s cannot be performed on relation "%s"`, operation, d.Name())
			detail = "This operation is not supported for sequences."
		}
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: message, Detail: detail}
}

// These sources admit semantic planning before checking execution-time authority.
// Other virtual catalogs retain their existing mutation boundaries.
func GuardedCatalogRelation(d *catalog.Relation) bool {
	if d == nil || d.RelationKind != catalog.CatalogRelation || d.Address().ClassID != ids.RelationClassID {
		return false
	}
	return d.Address().ObjectID == pgcatalog.AggregateOID || d.Address().ObjectID == pgcatalog.AMOID || d.Address().ObjectID == pgcatalog.TypeOID || d.Address().ObjectID == pgcatalog.TSConfigOID || d.Address().ObjectID == pgcatalog.TransformOID
}

// Reachability is independent of catalog binding and precedes relation admission.
func ValidateMergeReachability(s *parser.Merge) error {
	var unconditional [3]bool
	for _, arm := range s.Arms {
		if unconditional[arm.Match] {
			return sqlError("42601", "unreachable WHEN clause specified after unconditional WHEN clause")
		}
		if arm.Condition == nil {
			unconditional[arm.Match] = true
		}
	}
	return nil
}
