package core

import (
	"context"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/stats"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The statistics import runtime's host is the statement host; AuthorizationView
// is its authority. Operation, Context, Environments, PendingCatalog,
// EngineStorageLocked and the Engine.Mu pair are shared with exec.Host and the
// EXPLAIN renderer, RoutineAuthorizationLocked with the contrib runtimes.
var _ stats.Host[AuthorizationView] = (*statementHost)(nil)

// AttributeStatistics and RelationStatistics are exec.Host's statistics
// import functions.
func (h *statementHost) AttributeStatistics(clear, variadic bool, values []sqltype.Value) (bool, error) {
	return stats.ImportAttribute[AuthorizationView](h, clear, variadic, values)
}

func (h *statementHost) RelationStatistics(clear, variadic bool, values []sqltype.Value) (bool, error) {
	return stats.ImportRelation[AuthorizationView](h, clear, variadic, values)
}

// Statistics visitors share the catalog capture frontier and immutable facts.
func (r *catalogRuntime) VisitAttributeStatistics(ctx context.Context, source *catalog.Relation, visit func([]sqltype.Value) error) error {
	return stats.VisitAttributes(ctx, r.auth, &r.engine.Storage, source, visit)
}

func (r *catalogRuntime) VisitExtendedStatistics(ctx context.Context, visit func(ids.OID, bool, sqltype.Value, sqltype.Value, sqltype.Value, sqltype.Value) error) error {
	return stats.VisitExtended(ctx, r.auth, &r.engine.Storage, visit)
}

func (r *catalogRuntime) VisitExtendedStatisticsView(ctx context.Context, source *catalog.Relation, visit func([]sqltype.Value) error) error {
	return stats.VisitExtendedView(ctx, r.auth, &r.engine.Storage, source, r.rt.Presentation, r.rt.Temporary, visit)
}

// BindStatisticsExpression binds with the index expression binder over parent.
func (h DDLHost) BindStatisticsExpression(a ddl.Authority, parent *catalog.Relation, raw parser.Expr, input sqltype.InputEnvironment) (catalog.StatisticsExpression, error) {
	auth := DDLAuthorityOf(a)
	return stats.BindExpression(h.Op, h.E.IndexExpressionBinder(h.Op, input, auth, parent), &auth, parent, raw)
}

func (h DDLHost) SameStatisticsExpression(a, b catalog.StatisticsExpression) bool {
	return stats.SameExpression(a, b)
}

func (h *statementHost) ResolveRelationLocked(auth AuthorizationView, schema, name string) (ids.ObjectRef, error) {
	return h.scope().Engine().ResolveRelationReference(auth, parser.ObjectName{Parts: []parser.Identifier{{Text: schema}, {Text: name}}}, true)
}

func (h *statementHost) VacuumAllowedLocked(auth AuthorizationView, relation *catalog.Relation) (bool, error) {
	return table.VacuumAllowed(h.scope().executionContext(), auth.View, auth, relation)
}

// AcquireObjectLock takes a statement lock on ref with nothing held, reserving
// the replacement read like a sequence acquisition.
func (h *statementHost) AcquireObjectLock(ref ids.ObjectRef, mode lock.Mode) error {
	return h.scope().AcquireSequence(ref, mode, true)
}

func (h *statementHost) PublishedRelationLocked(address ids.ObjectAddress) (*catalog.Relation, bool) {
	return h.scope().Engine().Catalog.State.Relation(address)
}

// StatementCatalogLocked is the statement's private catalog journal, cloned
// from the pending one on its first write.
func (h *statementHost) StatementCatalogLocked() *session.Journal {
	s := h.scope()
	if s.Catalog == nil {
		s.Catalog = s.PendingCatalog().Clone()
	}
	return s.Catalog
}
