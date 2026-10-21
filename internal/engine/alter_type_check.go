package engine

import (
	"slices"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
)

// Reparse original syntax against the completed TYPE descriptor, rather than
// retagging old operators or retaining inferred coercions from the old usage.
func (e *Engine) rebindAlterTypeChecks(scope *StatementScope, op *session.Operation, auth AuthorizationView, ref ids.ObjectRef, next *catalog.Relation, queue []alterTypeCheck) (*catalog.Relation, []catalog.CheckConstraint, error) {
	if len(queue) == 0 {
		return next, nil, nil
	}
	replacement := *next
	replacement.Constraints.Check = slices.Clone(next.Constraints.Check)
	columns := make([]analyze.QueryColumn, len(next.Columns))
	for i, column := range next.Columns {
		columns[i] = analyze.QueryColumn{Descriptor: column, Slot: i, Leaf: 0, Physical: i}
	}
	qualifier := analyze.QueryQualifier{Name: next.Name(), Original: next.Name(), Schema: auth.Catalog.Get(next.Namespace()).Name(), Ref: ref, Columns: columns}
	namespace := analyze.QueryNamespace{Columns: columns, Qualifiers: []analyze.QueryQualifier{qualifier}, Frame: next.Columns}
	var ordered []catalog.CheckConstraint
	for _, pending := range queue {
		// Parse in CHECK context so selected calls remain functions, rather than
		// the general scalar grammar's function-style casts. Only the extracted
		// expression is bound; this wrapper never creates catalog metadata.
		syntax, err := parser.ParseWithOptions("CREATE TABLE "+catalog.QuoteIdentifier(next.Name())+" (CHECK ("+pending.sql+"))", parser.Options{Context: op.Context(), StandardConformingStrings: true})
		if err != nil {
			return nil, nil, alterTypeCheckReparseError(err)
		}
		e.mu.Lock()
		if ref.Epoch != e.catalog.state.Epoch() {
			e.mu.Unlock()
			return nil, nil, ids.ErrCatalogReplaced
		}
		binder := newExpressionBinder(op.Context(), e, scope.input, op, nil)
		binder.Consumer.CheckConsumer, binder.Params.NoParameters, binder.Query.Auth = true, true, &auth
		binder.Stored.CheckLiterals = pending.literals
		binder.Query.Namespace = &namespace
		binder.Query.Sources = []exec.Leaf{{Ref: ref, Relation: &replacement}}
		node, err := binder.BindBoolean(syntax[0].(*parser.CreateTable).Elements[0].Check.Expression, analyze.ProjectionContext, "CHECK")
		e.mu.Unlock()
		if err != nil {
			return nil, nil, alterTypeCheckReparseError(err)
		}
		definition := replacement.Constraints.Check[pending.index]
		definition.OID = 0 // Affected TYPE definitions get a new logical identity.
		definition.Expression, definition.Dependencies, definition.CommentSnapshot, definition.VisibilitySnapshot = node, nil, false, false
		var dependencies []ids.ObjectRef
		if _, err := (stored.ScalarRenderer{Definition: op.Definition, ACLOutput: regobj.ACLItemOutput{Owner: auth.Owner, Roles: auth.Roles}, Owner: &replacement, Dependencies: &dependencies, CommentSnapshot: &definition.CommentSnapshot, VisibilitySnapshot: &definition.VisibilitySnapshot}).SQL(op.Context(), auth.Catalog, node); err != nil {
			return nil, nil, err
		}
		for _, dependency := range dependencies {
			address := dependency.Address
			address.SubID = 0
			revision := replacement.Revision()
			if descriptor, exists := auth.Catalog.Descriptor(address); exists {
				revision = descriptor.Revision()
			}
			definition.Dependencies = append(definition.Dependencies, catalog.ExpressionDependency{Ref: dependency, Revision: revision})
		}
		replacement.Constraints.Check[pending.index] = definition
	}
	if err := stored.RefreshGenerationChecks(&replacement, auth.Catalog); err != nil {
		return nil, nil, err
	}
	for _, pending := range queue {
		definition := replacement.Constraints.Check[pending.index]
		if !definition.NotValid {
			ordered = append(ordered, definition)
		}
	}
	return &replacement, ordered, nil
}
