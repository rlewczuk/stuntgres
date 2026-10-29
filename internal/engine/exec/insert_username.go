package exec

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// EvalInsertUsername authenticates the selected installed body independently of
// structural native-trigger classification. Scalar calls never borrow a frame.
func (c *Context) EvalInsertUsername(n plan.InsertUsernameExpr) (sqltype.Value, error) {
	if err := EvaluationContext(c); err != nil { return sqltype.Value{}, err }
	if err := n.ValidateStoredProvider(); err != nil { return sqltype.Value{}, err }
	auth, _, err := RoutineAdmissionAuthority(c)
	if err != nil { return sqltype.Value{}, err }
	if !auth.Live() || n.Ref.Domain() != auth.Catalog.Owner().Domain {
		return sqltype.Value{}, fmt.Errorf("stuntgres: insert_username lacks current routine authority")
	}
	ref := n.Ref
	if ref.Epoch != auth.Catalog.Epoch() {
		if c.RoutineConsumer == nil || !preparation.StoredReferenceValid(auth.Catalog, c.RoutineConsumer, ref) {
			return sqltype.Value{}, ids.ErrCatalogReplaced
		}
		ref.Epoch = auth.Catalog.Epoch()
	}
	current, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.RoutineReference.(*catalog.Procedure))
	if err != nil { return sqltype.Value{}, err }
	if ref.Address != current.Address() || !extension.StockInsertUsername.SupportsDMLTrigger(current) {
		return sqltype.Value{}, fmt.Errorf("stuntgres: invalid insert_username provider")
	}
	if err := extension.StockInsertUsername.ValidateProcedure(auth.Catalog, current); err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Value{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: "insert_username: not fired by trigger manager"}
}
