package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInstalledPgBuffercacheEviction(n plan.InstalledPgBuffercacheEvictionExpr, view catalog.Read) error {
	if !ReferenceEpochValid(view, nil, n.Ref) {
		return fmt.Errorf("stuntgres: invalid installed pg_buffercache eviction reference")
	}
	proc, _, err := currentInstalledProcedure(view, n.Ref, n.Procedure)
	if err != nil { return err }
	if n.CallRoutineReference() != n.Procedure || extension.PgBuffercacheProviderOf(proc) != extension.StockPgBuffercache {
		return fmt.Errorf("stuntgres: invalid installed pg_buffercache eviction carrier")
	}
	boundary := n.PgBuffercacheEviction
	boundary.RoutineReference = proc
	return extension.ValidatePgBuffercacheEvictionCall(view, proc, boundary)
}
