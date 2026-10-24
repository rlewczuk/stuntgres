package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledEarthPoint(n plan.InstalledEarthPointExpr, view catalog.Read) error {
	_, err := currentEarthPointProcedure(n, view)
	return err
}

func currentEarthPointProcedure(n plan.InstalledEarthPointExpr, view catalog.Read) (*catalog.Procedure, error) {
	captured, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil {
		return nil, err
	}
	if captured == nil || captured != n.Procedure {
		return nil, fmt.Errorf("stuntgres: invalid stored earth point carrier")
	}
	current, symbol, err := currentInstalledProcedure(view, n.Ref, captured)
	if err != nil {
		return nil, err
	}
	if symbol != "geo_distance" {
		return nil, fmt.Errorf("stuntgres: invalid stored earth point operation")
	}
	boundary := functions.EarthPoint{Info: expr.Info{Usage: n.ResultUsage(), RoutineReference: current}, Plan: n.Plan, Args: n.Args}
	if err := extension.ValidateEarthPointCall(view, current, boundary); err != nil {
		return nil, err
	}
	if err := extension.StockEarthdistance.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}
