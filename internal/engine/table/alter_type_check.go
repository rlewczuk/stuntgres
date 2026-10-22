package table

import (
	"errors"
	"fmt"
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Original definitions are captured once after DROP, before any TYPE changes
// their usages. Queue order is TYPE discovery, then retained declaration order.
type AlterTypeCheck struct {
	Index    int
	SQL      string
	Literals []plan.LiteralExpr
}

func CaptureAlterTypeChecks(op *session.Operation, view catalog.Read, original, survivor *catalog.Relation, actions []parser.AlterTableAction) ([]AlterTypeCheck, error) {
	if len(survivor.Constraints.Check) == 0 || !slices.ContainsFunc(actions, func(a parser.AlterTableAction) bool { return a.Action == parser.AlterColumnType }) {
		return nil, nil
	}
	ordered := OrderedChecks(survivor)
	var queue []AlterTypeCheck
	for _, action := range actions {
		if action.Action != parser.AlterColumnType {
			continue
		}
		slot, exists := original.Index[action.Column.Text]
		if !exists {
			continue // The TYPE owner supplies the missing-column diagnostic.
		}
		attribute := original.Attributes[slot]
		for _, check := range ordered {
			affected := false
			for _, dependency := range check.Dependencies {
				if dependency.Ref.Address.ClassID == survivor.Address().ClassID && dependency.Ref.Address.ObjectID == survivor.Address().ObjectID && dependency.Ref.Address.SubID == attribute {
					affected = true
					break
				}
			}
			if !affected {
				continue
			}
			index := slices.IndexFunc(survivor.Constraints.Check, func(c catalog.CheckConstraint) bool { return c.Ordinal == check.Ordinal })
			if slices.ContainsFunc(queue, func(c AlterTypeCheck) bool { return c.Index == index }) {
				continue
			}
			var literals []plan.LiteralExpr
			canonical, err := (stored.ScalarRenderer{Definition: op.Definition, Owner: survivor, CheckLiterals: &literals}).SQL(op.Context(), view, check.Expression)
			if err != nil {
				return nil, err
			}
			queue = append(queue, AlterTypeCheck{Index: index, SQL: canonical, Literals: literals})
		}
	}
	return queue, nil
}

func AlterTypeCheckReparseError(err error) error {
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) {
		copy := *diagnostic
		copy.Position, copy.InternalPosition, copy.InternalQuery = "", "", ""
		return sqlstate.InputDiagnostic(err, &copy)
	}
	return err
}

// One completed-row validator is shared by TYPE rewriting and metadata-only NN
// scans. The CHECK traversal descriptor is ephemeral; storage stays alphabetic.
type AlterRowValidator struct {
	relation *catalog.Relation
	Checks   *catalog.Relation
	schema   string
	NotNull  []int
}

func (v AlterRowValidator) Validate(row []sqltype.Value, evaluation *exec.Context) error {
	for _, slot := range v.NotNull {
		column := v.relation.Columns[slot]
		value, err := LogicalColumnValue(v.relation, slot, row, evaluation)
		if err != nil {
			return err
		}
		if value.IsNull() {
			diagnostic := sqlError("23502", fmt.Sprintf(`column "%s" of relation "%s" contains null values`, column.Name, v.relation.Name())).(*sqlstate.Error)
			diagnostic.Schema, diagnostic.Table, diagnostic.Column = v.schema, v.relation.Name(), column.Name
			return diagnostic
		}
	}
	if v.Checks == nil {
		return nil
	}
	failed, err := ValidateCheckRow(v.Checks, row, evaluation)
	if err != nil || failed == nil {
		return err
	}
	diagnostic := sqlError("23514", fmt.Sprintf(`check constraint "%s" of relation "%s" is violated by some row`, failed.Name, v.relation.Name())).(*sqlstate.Error)
	diagnostic.Schema, diagnostic.Table, diagnostic.Constraint = v.schema, v.relation.Name(), failed.Name
	return diagnostic
}

func NewAlterRowValidator(relation *catalog.Relation, schema string) AlterRowValidator {
	v := AlterRowValidator{relation: relation, schema: schema}
	for slot, column := range relation.Columns {
		if column.Nullable {
			continue
		}
		invalid := false
		for _, n := range relation.Constraints.NotNull {
			if n.Attribute == relation.Attributes[slot] {
				invalid = n.NotValid
				break
			}
		}
		if !invalid {
			v.NotNull = append(v.NotNull, slot)
		}
	}
	return v
}

func NeedsNotNullScan(original, next *catalog.Relation) bool {
	for _, n := range next.Constraints.NotNull {
		if n.NotValid {
			continue
		}
		valid := false
		for _, old := range original.Constraints.NotNull {
			if old.Attribute == n.Attribute {
				valid = !old.NotValid
				break
			}
		}
		if !valid {
			return true
		}
	}
	return false
}

// Reparse original syntax against the completed TYPE descriptor, rather than
// retagging old operators or retaining inferred coercions from the old usage.
func RebindAlterTypeChecks(h AlterHost, auth ddl.Authority, ref ids.ObjectRef, next *catalog.Relation, queue []AlterTypeCheck) (*catalog.Relation, []catalog.CheckConstraint, error) {
	if len(queue) == 0 {
		return next, nil, nil
	}
	view := h.AccessView(auth)
	replacement := *next
	replacement.Constraints.Check = slices.Clone(next.Constraints.Check)
	var ordered []catalog.CheckConstraint
	for _, pending := range queue {
		// Parse in CHECK context so selected calls remain functions, rather than
		// the general scalar grammar's function-style casts. Only the extracted
		// expression is bound; this wrapper never creates catalog metadata.
		syntax, err := parser.ParseWithOptions("CREATE TABLE "+catalog.QuoteIdentifier(next.Name())+" (CHECK ("+pending.SQL+"))", parser.Options{Context: h.Context(), StandardConformingStrings: true})
		if err != nil {
			return nil, nil, AlterTypeCheckReparseError(err)
		}
		if ref.Epoch != h.LiveEpoch() {
			return nil, nil, ids.ErrCatalogReplaced
		}
		node, err := h.RebindCheck(auth, &replacement, ref, syntax[0].(*parser.CreateTable).Elements[0].Check.Expression, pending.Literals)
		if err != nil {
			return nil, nil, AlterTypeCheckReparseError(err)
		}
		definition := replacement.Constraints.Check[pending.Index]
		definition.OID = 0 // Affected TYPE definitions get a new logical identity.
		definition.Expression, definition.Dependencies, definition.CommentSnapshot, definition.VisibilitySnapshot = node, nil, false, false
		var dependencies []ids.ObjectRef
		if _, err := (stored.ScalarRenderer{Definition: h.OperationDefinition(), ACLOutput: regobj.ACLItemOutput{Owner: view.Owner, Roles: view.Roles}, Owner: &replacement, Dependencies: &dependencies, CommentSnapshot: &definition.CommentSnapshot, VisibilitySnapshot: &definition.VisibilitySnapshot}).SQL(h.Context(), view.Catalog, node); err != nil {
			return nil, nil, err
		}
		for _, dependency := range dependencies {
			address := dependency.Address
			address.SubID = 0
			revision := replacement.Revision()
			if descriptor, exists := view.Catalog.Descriptor(address); exists {
				revision = descriptor.Revision()
			}
			definition.Dependencies = append(definition.Dependencies, catalog.ExpressionDependency{Ref: dependency, Revision: revision})
		}
		replacement.Constraints.Check[pending.Index] = definition
	}
	if err := stored.RefreshGenerationChecks(&replacement, view.Catalog); err != nil {
		return nil, nil, err
	}
	for _, pending := range queue {
		definition := replacement.Constraints.Check[pending.Index]
		if !definition.NotValid {
			ordered = append(ordered, definition)
		}
	}
	return &replacement, ordered, nil
}
