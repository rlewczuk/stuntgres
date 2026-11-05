package exec

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// Native SQL bodies initialize their own retained expressions only after an
// actual non-NULL outer entry. They cannot be initialized by closed workers or
// eagerly authorized merely because a nullable input could reach them.
func nativeBodyInitializationRequired(root plan.Node) bool {
	return nativeBodyRequired(root, false)
}

// XML2's structural wrappers require a live invocation even without simulation;
// their genuine aggregate support calls cannot borrow closed worker settings.
func xml2BodyInvocationRequired(root plan.Node) bool {
	return nativeBodyRequired(root, true)
}

func nativeBodyRequired(root plan.Node, xml2Only bool) bool {
	required := false
	var walk func(plan.Node)
	walk = func(node plan.Node) {
		if node == nil || required {
			return
		}
		plan.QueryExpressions(node, func(expression expr.Expr) {
			plan.WalkExpression(expression, func(selected expr.Expr) {
				declaration, err := ExpressionRoutineDescriptor(selected)
				if err != nil || declaration != nil && (xml2Only && extension.StockXML2.SupportsRoutineConfiguration(declaration) || !xml2Only && (len(declaration.Body) != 0 || declaration.DeclarationBody != nil)) {
					required = true
				}
			})
		})
		if aggregate, ok := node.(*plan.AggregateNode); ok && !aggregate.PresenceOnly {
			for _, call := range aggregate.Calls {
				selected, ok := call.Provider.(*TransitionAggregate)
				if !ok {
					continue
				}
				supports := [4]*catalog.Procedure{selected.Support, selected.Final, selected.InverseSupport}
				if selected.CombinePlan != nil {
					supports[3] = selected.CombinePlan.Support
				}
				for _, support := range supports {
					if support != nil && (xml2Only && extension.StockXML2.SupportsRoutineConfiguration(support) || !xml2Only && (support.RoutineDefinition() != nil || len(support.Body) != 0 || support.DeclarationBody != nil)) {
						required = true
						return
					}
				}
			}
		}
		left, right := plan.QueryInputs(node)
		walk(left)
		walk(right)
	}
	walk(root)
	return required
}

// captureClosedQuery is a LIVE coordinator initializer. The factory closes only
// immutable initialized callsites and captured presentation, never its evaluator.
func captureClosedQuery(ctx context.Context, root plan.Node, source *Source, parameters []sqltype.Value) (func() *EvaluationCapture, error) {
	factory := func() *EvaluationCapture { return &EvaluationCapture{} }
	host := source.LiveHost()
	if host == nil {
		return factory, nil
	}
	factory = host.CaptureParallelEvaluation()
	state := State{Source: source, Parameters: parameters}
	input, output := host.Environments()
	state.Input, state.Output = input, output
	evaluation := state.Evaluation(ctx)
	if evaluation.routineSourceError != nil {
		return nil, evaluation.routineSourceError
	}
	owner := evaluation.Catalog.Owner()
	if owner == nil || owner.Sepgsql == nil {
		return factory, nil
	}
	if nativeBodyInitializationRequired(root) {
		return nil, fmt.Errorf("stuntgres: conditional native SQL body requires serial initialization")
	}
	var initialize func(plan.Node) error
	initialize = func(node plan.Node) error {
		if node == nil {
			return nil
		}
		var failure error
		plan.QueryExpressions(node, func(expression expr.Expr) {
			if failure == nil {
				failure = AdmitExpression(&evaluation, expression, evaluation.Catalog)
			}
		})
		if failure != nil {
			return failure
		}
		// Ordering and grouping kernels consume their selected support leaves, not
		// the ordinary expressions enumerated above. Certify only those retained
		// sources while the coordinator still owns a live initializer.
		switch selected := node.(type) {
		case *plan.SortNode:
			if !selected.PresenceOnly {
				for _, key := range selected.Keys {
					if err := key.Comparison.VisitIndexSupports(func(support sqltype.IndexSupport, slot uint16) error {
						return InitializeIndexSupport(&evaluation, support, slot)
					}); err != nil {
						return err
					}
				}
			}
		case *plan.AggregateNode:
			if !selected.PresenceOnly {
				for _, call := range selected.Calls {
					if supports, ok := call.Provider.(interface {
						VisitIndexSupports(func(sqltype.IndexSupport, uint16) error) error
					}); ok {
						if err := supports.VisitIndexSupports(func(support sqltype.IndexSupport, slot uint16) error {
							return InitializeIndexSupport(&evaluation, support, slot)
						}); err != nil {
							return err
						}
					}
				}
				for _, group := range selected.Grouping {
					if group.PrivateTree {
						continue
					}
					if err := InitializeQueryEqualityRoutine(&evaluation, group.Routine, group.Procedure, group.RoutineRecipe, group.Source, GroupingEqualityInitialization); err != nil {
						return err
					}
					if err := InitializeGroupingCallbacks(&evaluation, group); err != nil {
						return err
					}
				}
			}
		}
		left, right := plan.QueryInputs(node)
		if err := initialize(left); err != nil {
			return err
		}
		return initialize(right)
	}
	if err := initialize(root); err != nil {
		return nil, err
	}
	proof, err := host.SealRoutineInitialization(evaluation.RoutineInitialization, evaluation.Catalog)
	if err != nil {
		return nil, err
	}
	capturedFactory := factory
	return func() *EvaluationCapture { capture := capturedFactory(); capture.Routines = proof; return capture }, nil
}
