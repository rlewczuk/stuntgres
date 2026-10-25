package core

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/view"
)

// Historical validation initializes only the selected own programme; ordinary
// conversions retain the ancestor traversal above.
func PrepareOwnDomainChecks(c *exec.Context, d *catalog.Type) error {
	scope := ScopeOf(c.LiveHost())
	if scope != nil {
		c.PreparedDomainChecks = scope.Domains.PreparedChecks
		c.DomainPhase = ids.CommandIdentity{Transaction: scope.TxRef, Command: scope.CommandID}
	}
	if len(d.DomainChecks) == 0 {
		return nil
	}
	var auth AuthorizationView
	if routineAuth := RoutineAuthorityView(c); routineAuth != nil {
		auth = *routineAuth
	} else if scope != nil {
		auth = scope.RoutineAuth
	}
	// Only the existing trusted RI phase establishes owner provenance. A
	// borrowed local capture for input/view admission is still an invoker phase.
	ri := scope != nil && scope.ForeignActionAuth != nil &&
		scope.ForeignActionAuth.State.CurrentUser == auth.State.CurrentUser
	programme, exists := c.PreparedDomainChecks[d]
	for _, phase := range ProgramPhases(programme) {
		if phase.Number == c.DomainPhase && phase.Actor == auth.State.CurrentUser && phase.Ri == ri {
			return nil // Same initialized programme phase; never a per-row ACL lookup.
		}
	}
	phase := DomainCheckPhase{Number: c.DomainPhase, Actor: auth.State.CurrentUser, Ri: ri}
	if exists {
		if err := phase.Admission.InitializePhase(&ProgramPhases(programme)[0].Admission, RoutineAccessOf(auth)); err != nil {
			return err
		}
	} else {
		for i, check := range d.DomainChecks {
			checkEvaluation := *c
			checkEvaluation.RoutineRecipe, checkEvaluation.RoutineGraft = true, false
			checkEvaluation.RoutineChecks = check.Dependencies
			checkEvaluation.RoutineReferences, checkEvaluation.RoutineConsumer = nil, nil
			checkEvaluation.RoutineProgram = &phase.Admission
			node, changed, err := exec.PreflightCoercionsWithPolicy(&checkEvaluation, check.Expression, true)
			c.PreparedDomainChecks = checkEvaluation.PreparedDomainChecks
			if err != nil {
				return err
			}
			if err := view.ValidateExpressionJoinExecution(&checkEvaluation, node, domainCatalog(&checkEvaluation)); err != nil {
				return err
			}
			if changed {
				if programme.Expressions == nil {
					programme.Expressions = make([]expr.Expr, len(d.DomainChecks))
					for j := range d.DomainChecks {
						programme.Expressions[j] = d.DomainChecks[j].Expression
					}
				}
				programme.Expressions[i] = node
			}
		}
	}
	if program := c.RoutineProgram; program != nil && scope == nil {
		program.Include(&phase.Admission)
	}
	programme.Phases = append(ProgramPhases(programme), phase)
	if c.PreparedDomainChecks == nil {
		c.PreparedDomainChecks = make(map[*catalog.Type]exec.DomainCheckProgram)
	}
	c.PreparedDomainChecks[d] = programme
	if scope != nil {
		scope.Domains.PreparedChecks = c.PreparedDomainChecks
	}
	return nil
}

func domainCatalog(c *exec.Context) catalog.Read {
	if c.Catalog.State() != nil {
		return c.Catalog
	}
	if scope := ScopeOf(c.LiveHost()); scope != nil {
		if scope.Domains.InputCatalog.State() != nil {
			return scope.Domains.InputCatalog
		}
		return scope.ResolutionCatalog()
	}
	return catalog.Read{}
}
