package engine

import (
	"slices"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// Reparse original syntax against the completed TYPE descriptor, rather than
// retagging old operators or retaining inferred coercions from the old usage.
func (e *Engine) rebindAlterTypeChecks(scope *StatementScope, op *session.Operation, auth AuthorizationView, ref ids.ObjectRef, next *catalog.Relation, queue []table.AlterTypeCheck) (*catalog.Relation, []catalog.CheckConstraint, error) {
	if len(queue) == 0 {
		return next, nil, nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	h := alterHost{ddlHost{e: e, op: op}, scope}
	replacement := *next
	replacement.Constraints.Check = slices.Clone(next.Constraints.Check)
	var ordered []catalog.CheckConstraint
	for _, pending := range queue {
		// Parse in CHECK context so selected calls remain functions, rather than
		// the general scalar grammar's function-style casts. Only the extracted
		// expression is bound; this wrapper never creates catalog metadata.
		syntax, err := parser.ParseWithOptions("CREATE TABLE "+catalog.QuoteIdentifier(next.Name())+" (CHECK ("+pending.SQL+"))", parser.Options{Context: op.Context(), StandardConformingStrings: true})
		if err != nil {
			return nil, nil, table.AlterTypeCheckReparseError(err)
		}
		if ref.Epoch != h.LiveEpoch() {
			return nil, nil, ids.ErrCatalogReplaced
		}
		node, err := h.RebindCheck(auth, &replacement, ref, syntax[0].(*parser.CreateTable).Elements[0].Check.Expression, pending.Literals)
		if err != nil {
			return nil, nil, table.AlterTypeCheckReparseError(err)
		}
		definition := replacement.Constraints.Check[pending.Index]
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
		replacement.Constraints.Check[pending.Index] = definition
	}
	if err := stored.RefreshGenerationChecks(&replacement, auth.Catalog); err != nil {
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
