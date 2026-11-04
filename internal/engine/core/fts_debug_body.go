package core

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core/tsearch"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// DebugTextSearch receives the exact selected occurrence and borrowed evaluator.
// Neither the text-search host nor its materialized result remembers a callee or
// subject. The outer kernel has already evaluated arguments and entered it.
func (h *textSearchHost) DebugTextSearch(ctx context.Context, evaluator *exec.Context, info expr.Info, explicit bool, value sqltype.Value, text string, result *sqltype.Descriptor) (rows []sqltype.Value, err error) {
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if evaluator == nil || ScopeOf(evaluator.LiveHost()) != h.host().scope() {
		return nil, fmt.Errorf("stuntgres: missing original debug invocation authority")
	}
	local := *evaluator
	reference, recipe := info.RoutineReference, info.RoutineRecipeReference
	if recipe != nil && (!local.RoutineGraft || local.RoutineGraftSource != recipe) || recipe == nil && local.RoutineGraft {
		if err = exec.SelectExpressionRecipe(&local, recipe); err != nil {
			return nil, err
		}
	}
	outer, ok := reference.(*catalog.Procedure)
	expected := ids.OID(6184)
	if explicit {
		expected = 6183
	}
	if !ok || outer == nil || outer.Builtin == nil || outer.Builtin.Identity.ID != expected {
		return nil, fmt.Errorf("stuntgres: invalid original debug routine declaration")
	}
	if explicit {
		if err = initializeDebugBody(&local, info, outer); err != nil {
			return nil, err
		}
		return tsearch.Debug[AuthorizationView](h.host(), ctx, true, value, text, result)
	}
	local, err = exec.BeginNativeBody(&local, outer, info)
	if err != nil {
		return nil, err
	}
	defer func() { err = exec.SQLRoutineStatementContext(err, "ts_debug", 1) }()
	auth, dependencies, err := exec.RoutineInvocationAuthority(&local)
	if err != nil {
		return nil, err
	}
	configType, ok := auth.Catalog.LookupType(sqltype.Regconfig)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing original debug configuration type")
	}
	// The real original INTERNAL getter has no trusted fmgr wrapper. Its execute
	// hook belongs to the initialized outer body; execute its genuine GUC kernel.
	getter, err := debugBodyDeclaration(&local, auth, dependencies, outer, 3759)
	if err != nil {
		return nil, err
	}
	inner, err := debugBodyDeclaration(&local, auth, dependencies, outer, 6183)
	if err != nil {
		return nil, err
	}
	var innerInfo expr.Info
	for ordinal, edge := range outer.Body {
		selected := info
		selected.RoutineReference = edge.Routine
		selected.Occurrence = uint32(ordinal + 1)
		if edge.Routine.Address() == inner.Address() {
			innerInfo = selected
		}
		if err = exec.InitializeNativeBodyCall(&local, edge.Routine, selected); err != nil {
			return nil, err
		}
	}
	if getter == nil || innerInfo.RoutineReference == nil {
		return nil, fmt.Errorf("stuntgres: missing actual debug body occurrence")
	}
	value, err = h.CurrentTextSearchConfig(ctx, configType)
	if err != nil {
		return nil, err
	}
	textType, ok := auth.Catalog.LookupType(sqltype.Text)
	if !ok {
		return nil, fmt.Errorf("stuntgres: missing original debug document type")
	}
	token, err := local.FunctionEnterRoutine(inner, recipe, expr.RoutineBodyInputs{Count: 2, First: value.Type(), Second: textType, Occurrence: innerInfo.Occurrence, Source: innerInfo.Source})
	if err != nil {
		return nil, err
	}
	defer token.Leave()
	if err = initializeDebugBody(&local, innerInfo, inner); err != nil {
		return nil, err
	}
	return tsearch.Debug[AuthorizationView](h.host(), ctx, true, value, text, result)
}
func initializeDebugBody(c *exec.Context, info expr.Info, outer *catalog.Procedure) error {
	if err := exec.AdmitDebugCatalogBody(c, outer); err != nil {
		return exec.SQLRoutineStatementContext(err, "ts_debug", 1)
	}
	return admitInquiryBody(c, info)
}
func debugBodyDeclaration(c *exec.Context, auth exec.RoutineAccess, dependencies []catalog.ExpressionDependency, outer *catalog.Procedure, oid ids.OID) (*catalog.Procedure, error) {
	for _, edge := range outer.Body {
		if edge.Routine != nil && edge.Routine.Address().ObjectID == oid {
			return exec.BodyRoutineDescriptor(c, auth, dependencies, edge)
		}
	}
	return nil, fmt.Errorf("stuntgres: missing original debug body declaration")
}
