package engine

import (
	"fmt"
	"math"
	"slices"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
)

const restoreAttributeRankBoundary = "restore accepts only canonical pg_attribute rank repairs"

// Restore alone calls this interpreter, against its isolated catalog. It is not
// an UPDATE execution path and never grants writable access to catalog sources.
func (e *Engine) restoreAttributeRank(op *Operation, raw *parser.Update) error {
	name, column, rank, err := restoreAttributeRankSyntax(raw)
	if err != nil {
		return err
	}
	if err = op.Context().Err(); err != nil {
		return err
	}
	auth, err := op.Authorization()
	if err != nil {
		return err
	}
	actor, err := auth.roles.Descriptor(auth.state.CurrentUser)
	if err != nil {
		return err
	}
	if !actor.Superuser {
		return sqlError("42501", "permission denied for table pg_attribute")
	}
	e.mu.Lock()
	if e.importTargets == nil || auth.engine != e || auth.session != op.session || auth.roleRecord != e.roles || auth.catalog.owner == nil || auth.catalog.owner.catalog.domain != e.catalog.domain || auth.catalog.state != e.catalog.state || auth.catalog.Epoch() != e.catalog.state.epoch {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore attribute rank authority")
	}
	bootstrap, err := auth.roles.Descriptor(auth.state.Authenticated)
	if err != nil || !bootstrap.Superuser {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: invalid trusted restore attribute rank authority")
	}
	ref, err := e.resolveRelationReference(auth, name, false)
	if err != nil {
		e.mu.Unlock()
		return err
	}
	old, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok || old.relationKind != tableRelation {
		e.mu.Unlock()
		return sqlError("0A000", restoreAttributeRankBoundary)
	}
	slot, exists := old.index[column]
	if !exists {
		e.mu.Unlock()
		return sqlError("42703", fmt.Sprintf(`column "%s" of relation "%s" does not exist`, column, old.name))
	}
	if rank != 0 && !old.columns[slot].Usage.Type.IsArray() {
		e.mu.Unlock()
		return sqlError("0A000", restoreAttributeRankBoundary)
	}
	if old.columns[slot].DeclaredDimensions == rank {
		e.mu.Unlock()
		return nil
	}
	if old.revision == ids.DefinitionRevision(^uint64(0)) {
		e.mu.Unlock()
		return fmt.Errorf("stuntgres: definition revision exhausted")
	}
	next := *old
	next.revision++
	next.columns = slices.Clone(old.columns)
	next.columns[slot].DeclaredDimensions = rank
	staged := e.BeginCatalogStage(auth.catalog)
	staged.authorizeLocked(auth, op.session)
	e.mu.Unlock()
	defer e.DiscardCatalogStage(staged)
	if err = staged.Replace(&next); err != nil {
		return err
	}
	prepared, err := e.ValidateCatalogStage(op.Context(), staged)
	if err != nil {
		return err
	}
	_, err = e.publishCatalogOperation(op, nil, prepared, PublicationToken{Epoch: ref.Epoch})
	return err
}

func restoreAttributeRankSyntax(raw *parser.Update) (parser.ObjectName, string, int16, error) {
	reject := func() (parser.ObjectName, string, int16, error) {
		return parser.ObjectName{}, "", 0, sqlError("0A000", restoreAttributeRankBoundary)
	}
	if len(raw.Name.Parts) != 2 || raw.Name.Parts[0].Text != "pg_catalog" || raw.Name.Parts[1].Text != "pg_attribute" || raw.Only || raw.Star || raw.WithRecursive || len(raw.With) != 0 || raw.Alias.Text != "" || raw.From != nil || raw.CurrentOf != nil || raw.Returning != nil || len(raw.Assignments) != 1 {
		return reject()
	}
	assignment := raw.Assignments[0]
	if assignment.Target.Text != "attndims" || len(assignment.Steps) != 0 {
		return reject()
	}
	value, ok := assignment.Value.(parser.LiteralExpr)
	if !ok || value.Value.Kind != parser.IntegerValue {
		return reject()
	}
	if value.Value.Integer < 0 || value.Value.Integer > math.MaxInt16 {
		return parser.ObjectName{}, "", 0, sqlError("22003", "attribute rank is out of range")
	}
	where, ok := raw.Where.(parser.LogicalExpr)
	if !ok || where.Op != parser.LogicalAnd {
		return reject()
	}
	relation, ok := restoreAttributeRankEquality(where.Left, "attrelid")
	if !ok {
		return reject()
	}
	cast, ok := relation.(parser.RegclassExpr)
	if !ok {
		return reject()
	}
	text, ok := cast.Child.(parser.LiteralExpr)
	if !ok || text.Value.Kind != parser.StringValue {
		return reject()
	}
	name, err := parser.ParseRegclassName(text.Value.Text)
	if err != nil || len(name.Parts) != 2 {
		return reject()
	}
	label, ok := restoreAttributeRankEquality(where.Right, "attname")
	if !ok {
		return reject()
	}
	literal, ok := label.(parser.LiteralExpr)
	if !ok || literal.Value.Kind != parser.StringValue {
		return reject()
	}
	return name, literal.Value.Text, int16(value.Value.Integer), nil
}

func restoreAttributeRankEquality(raw parser.Expr, column string) (parser.Expr, bool) {
	comparison, ok := raw.(parser.OperatorExpr)
	if !ok || len(comparison.Name.Parts) != 1 || comparison.Name.Parts[0].Text != "=" || len(comparison.Args) != 2 {
		return nil, false
	}
	left, ok := comparison.Args[0].(parser.ColumnExpr)
	if !ok || len(left.Name.Parts) != 1 || left.Name.Parts[0].Text != column {
		return nil, false
	}
	return comparison.Args[1], true
}
