package stored

import (
	"context"
	"fmt"
	"slices"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
)

// ValidateIndexMetadataExpression validates the frozen structural catalog value,
// not its executability under the latest SQL body. No rebound node or SQL text
// escapes this entry; all ordinary render and execution entry points stay strict.
func ValidateIndexMetadataExpression(ctx context.Context, view catalog.Read, parent *catalog.Relation, node expr.Expr, dependencies []ids.ObjectRef) error {
	if view.Owner() == nil || view.State() == nil || parent == nil || view.Get(parent.Address()) != parent {
		return fmt.Errorf("stuntgres: invalid index expression metadata capture")
	}
	for _, ref := range dependencies {
		if err := ctx.Err(); err != nil {
			return err
		}
		address := ref.Address
		address.SubID = 0
		target := view.Get(address)
		if !ReferenceEpochValid(view, parent, ref) || target == nil {
			return fmt.Errorf("stuntgres: stale index expression metadata dependency")
		}
		if ref.Address.SubID != 0 {
			relation, ok := target.(*catalog.Relation)
			if !ok || !relation.HasSupportedAttribute(ref.Address.SubID) {
				return fmt.Errorf("stuntgres: invalid index expression metadata attribute")
			}
		}
	}
	_, err := (ScalarRenderer{Validation: true, Owner: parent, CapturedManifest: dependencies, indexMetadata: true}).SQL(ctx, view, node)
	return err
}

func (r ScalarRenderer) indexMetadataRoutine(view catalog.Read, call plan.SQLRoutineExpr) (*catalog.Procedure, error) {
	if err := call.ValidateStoredProvider(); err != nil {
		return nil, err
	}
	captured := call.Procedure
	if !slices.Contains(r.CapturedManifest, call.Ref) || !ReferenceEpochValid(view, r.retainedConsumer(), call.Ref) || captured.Owner.ID() == 0 || captured.Owner != view.Owner().Principals.Principal(captured.Owner.ID()) {
		return nil, fmt.Errorf("stuntgres: invalid index metadata routine lifetime")
	}
	current, exists := view.Procedure(call.Ref.Address)
	if !exists || current.Signature != captured.Signature || current.RoutineKind != captured.RoutineKind || current.Result != captured.Result || !call.Signature.ValidFor(current.RoutineDefinition()) {
		return nil, fmt.Errorf("stuntgres: incompatible index metadata routine declaration")
	}
	if _, err := exec.ValidateStoredRoutineDescriptor(view, current); err != nil {
		return nil, err
	}
	r.record(call.Ref)
	return current, nil
}
