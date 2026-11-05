package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledPgBuffercacheAggregate(n plan.InstalledPgBuffercacheAggregateExpr, view catalog.Read) error {
	if !ReferenceEpochValid(view, nil, n.Ref) {
		return fmt.Errorf("stuntgres: invalid installed pg_buffercache aggregate reference")
	}
	proc, _, err := currentInstalledProcedure(view, n.Ref, n.Procedure)
	if err != nil { return err }
	if n.CallRoutineReference() != n.Procedure || extension.PgBuffercacheProviderOf(proc) != extension.StockPgBuffercache {
		return fmt.Errorf("stuntgres: invalid installed pg_buffercache aggregate carrier")
	}
	boundary := n.PgBuffercacheAggregate
	boundary.RoutineReference = proc
	return extension.ValidatePgBuffercacheAggregateCall(view, proc, boundary)
}