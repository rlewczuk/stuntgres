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

func ValidateInstalledCubeSpatial(n plan.InstalledCubeSpatialExpr, view catalog.Read) error {
	_, err := currentCubeSpatialProcedure(n, view)
	return err
}

func currentCubeSpatialProcedure(n plan.InstalledCubeSpatialExpr, view catalog.Read) (*catalog.Procedure, error) {
	captured, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil {
		return nil, err
	}
	if captured == nil || captured != n.Procedure {
		return nil, fmt.Errorf("stuntgres: invalid stored cube spatial carrier")
	}
	current, symbol, err := currentInstalledProcedure(view, n.Ref, captured)
	if err != nil {
		return nil, err
	}
	if symbol != n.Plan.Operation().Routine() {
		return nil, fmt.Errorf("stuntgres: invalid stored cube spatial operation")
	}
	// Validate current installation authority without grafting it onto the
	// original executable carrier, reference, or captured recipe.
	boundary := functions.CubeSpatial{
		Info: expr.Info{Usage: n.ResultUsage(), RoutineReference: current},
		Plan: n.Plan,
		Args: n.Args,
	}
	if err := extension.ValidateCubeSpatialCall(view, current, boundary); err != nil {
		return nil, err
	}
	if err := extension.StockCube.ValidateProcedure(view, current); err != nil {
		return nil, err
	}
	return current, nil
}
