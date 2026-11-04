package core

import (
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
)

// SimplifyGenerationDefinitionLocked retains the generation binder's conditional
// prewalk, borrowing its actual statement for active routine initialization.
// CREATE has a real stage but a prospective relation; ALTER may have no stage.
// Entry and return hold Mu, including errors and panic unwinding. Constant work
// runs without Mu or the statement's catalog replacement reservation.
func (h DDLHost) SimplifyGenerationDefinitionLocked(auth AuthorizationView, evaluation *exec.Context, definition expr.Expr) (expr.Expr, error) {
	simplify := func() (expr.Expr, error) {
		node, _, err := exec.SimplifyConditional(evaluation, definition, true)
		return node, err
	}
	if h.E == nil {
		return nil, ids.ErrCatalogReplaced
	}
	if h.E.Sepgsql == nil {
		return simplify()
	}
	scope := auth.Statement
	if h.Op == nil || scope == nil || scope.Op != h.Op || scope.Engine() != h.E ||
		auth.Session != h.Op.Session || auth.Owner != &h.E.Catalog.Owner || auth.RoleRecord == nil ||
		auth.inputs != &h.E.Authority || auth.settings != h.Op.EffectiveSettings() ||
		!auth.Roles.BelongsTo(&h.E.RoleStore.Domain) || auth.PairedCatalog != auth.Catalog ||
		auth.Roles != access.RoleRead(&h.E.Catalog.Owner, &h.E.RoleStore.Domain, auth.Catalog, auth.RoleRecord) ||
		evaluation == nil || evaluation.Catalog != auth.Catalog || auth.Catalog.State() == nil ||
		auth.Catalog.Owner() != &h.E.Catalog.Owner || auth.Catalog.Domain() != h.E.Catalog.Domain ||
		auth.Catalog.Epoch() != h.E.Catalog.State.Epoch() || scope.Active() != nil || ActiveScopeOfLease(h.Op.Lease) != scope {
		return nil, ids.ErrCatalogReplaced
	}
	stage := h.CatalogStage
	var base catalog.Read
	var frontier [4]int
	// Check only the genuine structural input to this generation binding, not
	// unrelated stage deltas. Final publication retains their own observations.
	stageInput := func() ([4]int, bool) {
		var counts [4]int
		if stage == nil {
			return counts, true
		}
		if stage.Owner != h.E || stage.Lifecycle != CatalogOpen || stage.Invalid != nil ||
			stage.statement != scope || stage.AuthSession != h.Op.Session ||
			stage.Base.Owner() != auth.Catalog.Owner() || stage.Base.Domain() != auth.Catalog.Domain() || stage.Base.Epoch() != auth.Catalog.Epoch() {
			return counts, false
		}
		for address, descriptor := range stage.Descriptors() {
			counts[0]++
			if auth.Catalog.Get(address) != descriptor {
				return counts, false
			}
		}
		for name := range stage.Names() {
			counts[1]++
			if auth.Catalog.NameAddress(name) != stage.LookupName(name) {
				return counts, false
			}
		}
		for address := range stage.Removals() {
			counts[2]++
			if auth.Catalog.Get(address) != nil {
				return counts, false
			}
		}
		for name := range stage.OldNames() {
			counts[3]++
			if auth.Catalog.NameAddress(name) != stage.LookupName(name) {
				return counts, false
			}
		}
		return counts, true
	}
	var valid bool
	if frontier, valid = stageInput(); !valid {
		return nil, ids.ErrCatalogReplaced
	}
	if stage != nil {
		base = stage.Base
	}
	before, err := h.E.AuthorizationLocked(h.Op)
	if err != nil {
		return nil, err
	}
	catalogPredecessor, publishedRoles := h.E.Catalog.State, h.E.RoleStore.Published
	store := h.Op.EffectiveSettings()
	previousAuth, previousDependencies := scope.RoutineAuth, scope.RoutineDependencies
	previousHost, previousContext := evaluation.Host, evaluation.Context
	scope.RoutineAuth, scope.RoutineDependencies = auth, evaluation.RoutineChecks
	evaluation.Host = HostOf(scope)
	defer func() {
		scope.RoutineAuth, scope.RoutineDependencies = previousAuth, previousDependencies
		evaluation.Host, evaluation.Context = previousHost, previousContext
	}()
	scope.releaseCatalogReservation()
	h.E.Mu.Unlock()
	var node expr.Expr
	func() {
		defer h.E.Mu.Lock()
		evaluation.Context = scope.ActivateRuntime(evaluation.Context)
		// Initialize before SimplifyConditional copies contexts for recipes.
		err = exec.BeginRoutineInitialization(evaluation)
		if err == nil {
			node, err = simplify()
		}
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
	if scope.Active() != nil || scope.Op != h.Op || auth.Statement != scope ||
		ActiveScopeOfLease(h.Op.Lease) != scope || h.Op.EffectiveSettings() != store ||
		(stage != nil && stage.Base != base) {
		return nil, ids.ErrCatalogReplaced
	}
	if current, valid := stageInput(); !valid || current != frontier {
		return nil, ids.ErrCatalogReplaced
	}
	current, admissionErr := h.E.AuthorizationLocked(h.Op)
	if admissionErr != nil {
		return nil, admissionErr
	}
	// Never retry a fold after real effects or adopt a changed predecessor.
	if h.E.Catalog.State != catalogPredecessor || h.E.RoleStore.Published != publishedRoles ||
		current.Catalog.State() != before.Catalog.State() || current.Catalog.Domain() != before.Catalog.Domain() ||
		current.Catalog.Epoch() != before.Catalog.Epoch() || current.Revision != before.Revision ||
		current.RoleRecord != before.RoleRecord || current.State != before.State ||
		current.roleAuthority != before.roleAuthority || current.publishedRoleAuthority != before.publishedRoleAuthority {
		return nil, ids.ErrCatalogReplaced
	}
	return node, err
}
