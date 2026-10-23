package stored

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// ValidateDomainCheckDefinitions checks a domain's CHECK and NOT NULL
// ancestry, its CHECK records and their providers.
func ValidateDomainCheckDefinitions(view catalog.Read, domain *catalog.Type) error {
	base := domain.SQLType.DomainBase()
	if domain.SQLType.HasDomainChecks() != (len(domain.DomainChecks) != 0 || base.Type.HasDomainChecks()) {
		return fmt.Errorf("stuntgres: inconsistent domain CHECK ancestry")
	}
	if domain.SQLType.DomainNotNull() != (domain.SQLType.OwnDomainNotNull() || base.Type.DomainNotNull()) {
		return fmt.Errorf("stuntgres: inconsistent domain NOT NULL ancestry")
	}
	if !domain.SQLType.IsBuiltin() && domain.SQLType.OwnDomainNotNull() != (domain.DomainNotNull != nil) {
		return fmt.Errorf("stuntgres: inconsistent domain NOT NULL definition")
	}
	if nn := domain.DomainNotNull; nn != nil {
		if !domain.SQLType.OwnDomainNotNull() || nn.OID == 0 || nn.Name == "" || len(nn.Name) > 63 || !utf8.ValidString(nn.Name) || strings.IndexByte(nn.Name, 0) >= 0 {
			return fmt.Errorf("stuntgres: invalid domain NOT NULL definition")
		}
		for _, check := range domain.DomainChecks {
			if check.Name == nn.Name {
				return fmt.Errorf("stuntgres: duplicate domain constraint name")
			}
		}
	}
	previous := ""
	for _, check := range domain.DomainChecks {
		if check.OID == 0 || check.Name == "" || len(check.Name) > 63 || !utf8.ValidString(check.Name) || strings.IndexByte(check.Name, 0) >= 0 || previous != "" && previous >= check.Name || check.Ordinal < 0 || check.Expression == nil || check.Expression.ResultUsage().Type == nil || check.Expression.ResultUsage().Type.ID() != sqltype.Bool {
			return fmt.Errorf("stuntgres: invalid domain CHECK definition")
		}
		previous = check.Name
		if _, err := (ScalarRenderer{DomainValue: &base}).SQL(context.Background(), view, check.Expression); err != nil {
			return err
		}
		for _, dependency := range check.Dependencies {
			address := dependency.Ref.Address
			address.SubID = 0
			provider, exists := view.Descriptor(address)
			if !exists || dependency.Ref.Domain() != view.Owner().Domain || dependency.Ref.Epoch != view.Epoch() {
				return fmt.Errorf("stuntgres: invalid domain CHECK provider")
			}
			switch provider.Kind() {
			case ids.TypeObject, ids.CollationObject, ids.ProcedureObject, ids.RelationObject:
			case ids.OperatorObject:
				if err := validateCubeDomainCheckOperator(view, check.Expression, dependency.Ref); err != nil {
					return err
				}
			default:
				return fmt.Errorf("stuntgres: invalid domain CHECK provider")
			}
		}
	}
	return nil
}

// Selected cube operators are real CHECK dependencies, not aliases for their
// procedures. Admit only the exact checked stock member; this does not grant a
// generic arbitrary-operator domain provider capability.
func validateCubeDomainCheckOperator(view catalog.Read, node expr.Expr, ref ids.ObjectRef) error {
	if view.Owner() == nil || ref.Domain() != view.Owner().Domain || ref.Epoch != view.Epoch() || ref.Address.ClassID != ids.OperatorClassID || ref.Address.SubID != 0 {
		return fmt.Errorf("stuntgres: invalid domain CHECK operator reference")
	}
	operator, ok := view.Operator(ref.Address)
	if !ok {
		return fmt.Errorf("stuntgres: missing domain CHECK operator")
	}
	found := false
	var failure error
	plan.WalkExpression(node, func(child expr.Expr) {
		selected, ok := child.(plan.SelectedOperatorExpr)
		if !ok || selected.Ref != ref || failure != nil {
			return
		}
		capture, cube := cubeComparisonCaptureOf(selected.Provider)
		captured, ok := expr.InfoOf(selected.Provider).RoutineReference.(*catalog.Procedure)
		if !cube || !ok {
			return
		}
		if err := remapCubeComparisonRoutine(view, nil, captured, captured, capture, func(t *sqltype.Descriptor) *sqltype.Descriptor { return t }); err != nil {
			failure = err
			return
		}
		installation, exists := view.Extension(captured.Extension)
		if !exists || operator.Extension != installation.Address() || operator.Namespace() != installation.TargetNamespace || operator.Left != ids.OID(capture.left.ID()) || operator.Right != ids.OID(capture.right.ID()) || operator.Result != ids.OID(sqltype.Bool) || operator.Function != captured.Address().ObjectID {
			failure = fmt.Errorf("stuntgres: invalid domain CHECK cube operator declaration")
			return
		}
		for i := range 6 {
			if len(installation.Members) == extension.CubeMemberCount && installation.Members[3+i] == captured.Address() && installation.Members[10+i] == operator.Address() {
				found = true
				return
			}
		}
		failure = fmt.Errorf("stuntgres: invalid domain CHECK cube operator member")
	})
	if failure != nil {
		return failure
	}
	if !found {
		return fmt.Errorf("stuntgres: invalid domain CHECK operator provider")
	}
	return nil
}
