package engine

import (
	"context"
	"errors"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (c *EvalContext) EvaluateSelectedDomainCheck(view catalog.Read, selected *catalog.Type, value sqltype.Value, violation error) error {
	oldValue, oldUsage, oldActive, oldChecks := c.DomainValue, c.DomainFrameUsage, c.DomainValueActive, c.Input.DomainChecks
	c.Input.DomainChecks = c
	defer func() {
		c.DomainValue, c.DomainFrameUsage, c.DomainValueActive, c.Input.DomainChecks = oldValue, oldUsage, oldActive, oldChecks
	}()
	return c.evaluateOwnDomainChecks(view, selected, selected, value, violation)
}

func (c *EvalContext) EvalDomainValue(e plan.DomainValueExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if !c.DomainValueActive || c.DomainValue.Type() != e.Usage.Type || c.DomainFrameUsage != e.Usage {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing domain VALUE frame")
	}
	return c.DomainValue, nil
}

func (c *EvalContext) CheckDomain(ctx context.Context, target *sqltype.Descriptor, candidate sqltype.Value) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return preparation.CheckDomain(ctx, c, target, candidate)
}

func (c *EvalContext) EvaluateDomainChecks(view catalog.Read, target, d *catalog.Type, candidate sqltype.Value) error {
	base := d.SQLType.DomainBase()
	if base.Type.IsDomain() && base.Type.HasDomainChecks() {
		parent := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.Type.ID())})
		if err := c.EvaluateDomainChecks(view, target, parent, candidate); err != nil {
			return err
		}
	}
	return c.evaluateOwnDomainChecks(view, target, d, candidate, nil)
}

func (c *EvalContext) evaluateOwnDomainChecks(view catalog.Read, target, d *catalog.Type, candidate sqltype.Value, violation error) error {
	base := d.SQLType.DomainBase()
	if len(d.DomainChecks) == 0 {
		return nil
	}
	value, err := candidate.DomainValue(base.Type)
	if err != nil {
		return err
	}
	c.DomainValue, c.DomainFrameUsage, c.DomainValueActive = value, base, true
	expressions := c.PreparedDomainChecks[d].Expressions
	oldRecipe, oldGraft := c.RoutineRecipe, c.RoutineGraft
	oldChecks, oldReferences, oldConsumer := c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer
	defer func() {
		c.RoutineRecipe, c.RoutineGraft = oldRecipe, oldGraft
		c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer = oldChecks, oldReferences, oldConsumer
	}()
	for i, check := range d.DomainChecks {
		// Preserve the original CHECK recipe for lazily demanded bodies,
		// without changing the phase's actual invoker/RI authority.
		c.RoutineRecipe, c.RoutineGraft = true, false
		c.RoutineChecks, c.RoutineReferences, c.RoutineConsumer = check.Dependencies, nil, nil
		node := check.Expression
		if expressions != nil {
			node = expressions[i]
		}
		result, err := node.Eval(c)
		if err != nil {
			return err
		}
		truth, err := plan.TruthOf(result)
		if err != nil {
			return err
		}
		if truth == plan.False {
			if violation != nil {
				return violation
			}
			name := target.SQLType.SQLName()
			if c.Input.TypeDiagnosticName != nil {
				if captured := c.Input.TypeDiagnosticName(target.SQLType.ID()); captured != "" {
					name = captured
				}
			}
			diagnostic := sqlError("23514", fmt.Sprintf("value for domain %s violates check constraint %q", name, check.Name)).(*sqlstate.Error)
			diagnostic.Schema = view.Get(target.ObjectNamespace).Name()
			diagnostic.DataType, diagnostic.Constraint = target.Name(), check.Name
			return diagnostic
		}
	}
	return nil
}

// The existing domain map owns both prepared expressions and every actual phase
// initialization. These are consumed call graphs with original actors/lifetimes,
// not cached permission results. No CHECK truth is recorded or replayed.
type domainCheckProgram struct {
	Expressions []expr.Expr
	Phases      domainCheckPhases
}

// The runtime reads only a programme's expressions; its phases are an opaque
// slot that holds a domainPhaseList, which only engine helpers look inside.
type domainCheckPhases interface{ DomainCheckPhasesSlot() }

func (c *EvalContext) FunctionJSONBRecordInput() sqltype.InputEnvironment {
	input := c.Input
	input.DomainChecks = jsonbRecordDomainChecks{c}
	return input
}

// PostgreSQL saves a domain's own constraint violation, not errors raised while
// executing its CHECK expression. Keep that distinction across nested text
// composite/array input as well as JSON-node conversion.
type jsonbRecordDomainChecks struct{ evaluation *EvalContext }

type jsonbRecordHardDomainError struct{ error }

func (e jsonbRecordHardDomainError) Unwrap() error { return e.error }

func (c jsonbRecordDomainChecks) CheckDomain(ctx context.Context, target *sqltype.Descriptor, value sqltype.Value) error {
	err := c.evaluation.CheckDomain(ctx, target, value)
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var diagnostic *sqlstate.Error
	if errors.As(err, &diagnostic) && (diagnostic.Code == "23514" || diagnostic.Code == "23502") {
		view := preparation.DomainCatalog(c.evaluation)
		if view.State() != nil {
			d, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(target.ID())})
			if ok && d.SQLType == target && diagnostic.DataType == d.Name() && diagnostic.Schema == view.Get(d.ObjectNamespace).Name() {
				if diagnostic.Code == "23502" && diagnostic.Constraint == "" {
					return err
				}
				// Inherited checks report the requested domain, but retain their
				// declaring constraint name. Read only the captured generation.
				for current := d; ; {
					for _, check := range current.DomainChecks {
						if diagnostic.Constraint == check.Name {
							return err
						}
					}
					base := current.SQLType.DomainBase().Type
					if !base.IsDomain() {
						break
					}
					parent, found := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(base.ID())})
					if !found || parent.SQLType != base {
						break
					}
					current = parent
				}
			}
		}
	}
	// Soft-error classification deliberately examines the direct error, whereas
	// ordinary SQL diagnostics and cancellation follow this unchanged cause.
	return jsonbRecordHardDomainError{err}
}
