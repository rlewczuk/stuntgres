package stored

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
)

func ValidateInsertUsername(n plan.InsertUsernameExpr, v catalog.Read) error {
	if err := n.ValidateStoredProvider(); err != nil { return err }
	p := n.RoutineReference.(*catalog.Procedure)
	current, symbol, err := currentInstalledProcedure(v, n.Ref, p)
	if err != nil { return err }
	if symbol != "insert_username" || !extension.StockInsertUsername.SupportsDMLTrigger(current) {
		return fmt.Errorf("stuntgres: invalid stored insert_username provider")
	}
	return nil
}
