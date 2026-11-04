package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// PreflightIndexDefinitionLocked preserves the definition owner's ordered
// prewalk. An active client initializer borrows only its actual catalog-stage
// statement and suspends Mu while coercions or constant calls execute. The
// caller enters and leaves with Mu held, including callback panic unwinding.
func (h DDLHost) PreflightIndexDefinitionLocked(auth AuthorizationView, parent *catalog.Relation, evaluation *exec.Context, definition expr.Expr, simplify bool) (expr.Expr, error) {
	preflight := func() (expr.Expr, error) {
		node, _, err := exec.PreflightCoercionsWithPolicy(evaluation, definition, true)
		if err == nil && simplify {
			node, _, err = exec.SimplifyConditional(evaluation, node, true)
		}
		return node, err
	}
	if h.E.Sepgsql == nil {
		return preflight()
	}
	stage := h.CatalogStage
	if h.Op == nil || parent == nil || evaluation == nil || stage == nil || stage.Owner != h.E || stage.Lifecycle != CatalogOpen ||
		stage.statement == nil || stage.statement != auth.Statement || stage.AuthSession != h.Op.Session ||
		stage.Base.Owner() != auth.Catalog.Owner() || stage.Base.Domain() != auth.Catalog.Domain() || stage.Base.Epoch() != auth.Catalog.Epoch() {
		return nil, ids.ErrCatalogReplaced
	}
	scope := stage.statement
	if scope.Op != h.Op || scope.Active() != nil || ActiveScopeOfLease(h.Op.Lease) != scope ||
		evaluation.Catalog != auth.Catalog || auth.Catalog.Get(parent.Address()) != parent {
		return nil, ids.ErrCatalogReplaced
	}
	before, err := h.E.AuthorizationLocked(h.Op)
	if err != nil {
		return nil, err
	}
	base, store := stage.Base, h.Op.EffectiveSettings()
	previousAuth, previousDependencies := scope.RoutineAuth, scope.RoutineDependencies
	scope.RoutineAuth, scope.RoutineDependencies = auth, evaluation.RoutineChecks
	defer func() { scope.RoutineAuth, scope.RoutineDependencies = previousAuth, previousDependencies }()
	evaluation.Host = HostOf(scope)
	scope.releaseCatalogReservation()
	h.E.Mu.Unlock()
	var node expr.Expr
	func() {
		defer h.E.Mu.Lock()
		evaluation.Context = scope.ActivateRuntime(evaluation.Context)
		node, err = preflight()
	}()
	if canceled := h.Op.Context().Err(); canceled != nil {
		return nil, canceled
	}
	store.Mu.Lock()
	h.Op.Session.Mu.Lock()
	admissionErr := store.AdmitLocked(DomainOf(h.E), h.Op)
	h.Op.Session.Mu.Unlock()
	store.Mu.Unlock()
	if admissionErr != nil {
		return nil, admissionErr
	}
	if scope.Active() != nil || stage.statement != scope || scope.Op != h.Op || stage.AuthSession != h.Op.Session ||
		ActiveScopeOfLease(h.Op.Lease) != scope || h.Op.EffectiveSettings() != store ||
		stage.Lifecycle != CatalogOpen || stage.Base != base {
		return nil, ids.ErrCatalogReplaced
	}
	current, admissionErr := h.E.AuthorizationLocked(h.Op)
	if admissionErr != nil {
		return nil, admissionErr
	}
	// Do not rebind or replay a side-effecting prewalk after any predecessor or
	// SQL actor change. Normal stage publication retains its independent fences.
	if current.Catalog.State() != before.Catalog.State() || current.Catalog.Domain() != before.Catalog.Domain() ||
		current.Catalog.Epoch() != before.Catalog.Epoch() || current.Revision != before.Revision ||
		current.RoleRecord != before.RoleRecord || current.State != before.State ||
		h.E.StageRead(stage).Get(parent.Address()) != parent {
		return nil, ids.ErrCatalogReplaced
	}
	return node, err
}
