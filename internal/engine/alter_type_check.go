package engine

import (
	"fmt"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// Original definitions are captured once after DROP, before any TYPE changes
// their usages. Queue order is TYPE discovery, then retained declaration order.
type alterTypeCheck struct {
	index    int
	sql      string
	literals []literalExpression
}

func captureAlterTypeChecks(op *Operation, catalog CatalogRead, original, survivor *relationDescriptor, actions []parser.AlterTableAction) ([]alterTypeCheck, error) {
	if len(survivor.CheckConstraints) == 0 || !slices.ContainsFunc(actions, func(a parser.AlterTableAction) bool { return a.Action == parser.AlterColumnType }) {
		return nil, nil
	}
	ordered := orderedChecks(survivor)
	var queue []alterTypeCheck
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
			index := slices.IndexFunc(survivor.CheckConstraints, func(c checkConstraint) bool { return c.Ordinal == check.Ordinal })
			if slices.ContainsFunc(queue, func(c alterTypeCheck) bool { return c.index == index }) {
				continue
			}
			var literals []literalExpression
			canonical, err := (scalarRenderer{definition: op.definition, owner: survivor, checkLiterals: &literals}).sql(op.Context(), catalog, check.Expression)
			if err != nil {
				return nil, err
			}
			queue = append(queue, alterTypeCheck{index: index, sql: canonical, literals: literals})
		}
	}
	return queue, nil
}

// Reparse original syntax against the completed TYPE descriptor, rather than
// retagging old operators or retaining inferred coercions from the old usage.
func (e *Engine) rebindAlterTypeChecks(scope *StatementScope, op *Operation, auth AuthorizationView, ref ids.ObjectRef, next *relationDescriptor, queue []alterTypeCheck) (*relationDescriptor, []checkConstraint, error) {
	if len(queue) == 0 {
		return next, nil, nil
	}
	replacement := *next
	replacement.CheckConstraints = slices.Clone(next.CheckConstraints)
	columns := make([]queryColumn, len(next.Columns))
	for i, column := range next.Columns {
		columns[i] = queryColumn{descriptor: column, slot: i, leaf: 0, physical: i}
	}
	qualifier := queryQualifier{name: next.Name(), original: next.Name(), schema: auth.catalog.Get(next.Namespace()).Name(), ref: ref, columns: columns}
	namespace := queryNamespace{columns: columns, qualifiers: []queryQualifier{qualifier}, frame: next.Columns}
	var ordered []checkConstraint
	for _, pending := range queue {
		// Parse in CHECK context so selected calls remain functions, rather than
		// the general scalar grammar's function-style casts. Only the extracted
		// expression is bound; this wrapper never creates catalog metadata.
		syntax, err := parser.ParseWithOptions("CREATE TABLE "+quoteIdentifier(next.Name())+" (CHECK ("+pending.sql+"))", parser.Options{Context: op.Context(), StandardConformingStrings: true})
		if err != nil {
			return nil, nil, alterTypeCheckReparseError(err)
		}
		e.mu.Lock()
		if ref.Epoch != e.catalog.state.epoch {
			e.mu.Unlock()
			return nil, nil, ids.ErrCatalogReplaced
		}
		binder := newExpressionBinder(op.Context(), e, scope.input, op, nil)
		binder.checkConsumer, binder.noParameters, binder.queryAuth = true, true, &auth
		binder.checkLiterals = pending.literals
		binder.queryNamespace = &namespace
		binder.querySources = []queryLeaf{{ref: ref, relation: &replacement}}
		node, err := binder.bindBoolean(syntax[0].(*parser.CreateTable).Elements[0].Check.Expression, projectionContext, "CHECK")
		e.mu.Unlock()
		if err != nil {
			return nil, nil, alterTypeCheckReparseError(err)
		}
		definition := replacement.CheckConstraints[pending.index]
		definition.OID = 0 // Affected TYPE definitions get a new logical identity.
		definition.Expression, definition.Dependencies, definition.CommentSnapshot, definition.VisibilitySnapshot = node, nil, false, false
		var dependencies []ids.ObjectRef
		if _, err := (scalarRenderer{definition: op.definition, aclOutput: capturedACLItemOutput{owner: auth.engine, roles: auth.roles}, owner: &replacement, dependencies: &dependencies, commentSnapshot: &definition.CommentSnapshot, visibilitySnapshot: &definition.VisibilitySnapshot}).sql(op.Context(), auth.catalog, node); err != nil {
			return nil, nil, err
		}
		for _, dependency := range dependencies {
			address := dependency.Address
			address.SubID = 0
			revision := replacement.Revision()
			if descriptor, exists := auth.catalog.Descriptor(address); exists {
				revision = descriptor.Revision()
			}
			definition.Dependencies = append(definition.Dependencies, expressionDependency{Ref: dependency, Revision: revision})
		}
		replacement.CheckConstraints[pending.index] = definition
	}
	if err := refreshGenerationChecks(&replacement, auth.catalog); err != nil {
		return nil, nil, err
	}
	for _, pending := range queue {
		definition := replacement.CheckConstraints[pending.index]
		if !definition.NotValid {
			ordered = append(ordered, definition)
		}
	}
	return &replacement, ordered, nil
}

func alterTypeCheckReparseError(err error) error {
	if diagnostic, ok := err.(*sqlstate.Error); ok {
		copy := *diagnostic
		copy.Position, copy.InternalPosition, copy.InternalQuery = "", "", ""
		return &copy
	}
	return err
}

// One completed-row validator is shared by TYPE rewriting and metadata-only NN
// scans. The CHECK traversal descriptor is ephemeral; storage stays alphabetic.
type alterRowValidator struct {
	relation *relationDescriptor
	checks   *relationDescriptor
	schema   string
	notNull  []int
}

func (v alterRowValidator) validate(row []sqltype.Value, evaluation *EvalContext) error {
	for _, slot := range v.notNull {
		column := v.relation.Columns[slot]
		value, err := logicalColumnValue(v.relation, slot, row, evaluation)
		if err != nil {
			return err
		}
		if value.IsNull() {
			diagnostic := sqlError("23502", fmt.Sprintf(`column "%s" of relation "%s" contains null values`, column.Name, v.relation.Name())).(*sqlstate.Error)
			diagnostic.Schema, diagnostic.Table, diagnostic.Column = v.schema, v.relation.Name(), column.Name
			return diagnostic
		}
	}
	if v.checks == nil {
		return nil
	}
	failed, err := validateCheckRow(v.checks, row, evaluation)
	if err != nil || failed == nil {
		return err
	}
	diagnostic := sqlError("23514", fmt.Sprintf(`check constraint "%s" of relation "%s" is violated by some row`, failed.Name, v.relation.Name())).(*sqlstate.Error)
	diagnostic.Schema, diagnostic.Table, diagnostic.Constraint = v.schema, v.relation.Name(), failed.Name
	return diagnostic
}

func newAlterRowValidator(relation *relationDescriptor, schema string) alterRowValidator {
	v := alterRowValidator{relation: relation, schema: schema}
	for slot, column := range relation.Columns {
		if column.Nullable {
			continue
		}
		invalid := false
		for _, n := range relation.NotNullConstraints {
			if n.Attribute == relation.Attributes[slot] {
				invalid = n.NotValid
				break
			}
		}
		if !invalid {
			v.notNull = append(v.notNull, slot)
		}
	}
	return v
}

func needsNotNullScan(original, next *relationDescriptor) bool {
	for _, n := range next.NotNullConstraints {
		if n.NotValid {
			continue
		}
		valid := false
		for _, old := range original.NotNullConstraints {
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
