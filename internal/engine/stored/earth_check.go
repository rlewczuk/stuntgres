package stored

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func earthCheckSQL(view catalog.Read, n plan.EarthCheckExpr) (string, error) {
	if !n.Valid() {
		return "", fmt.Errorf("stuntgres: invalid stored earth CHECK")
	}
	base, ok := view.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(n.Value.Usage.Type.ID())})
	if !ok || base.SQLType != n.Value.Usage.Type {
		return "", fmt.Errorf("stuntgres: foreign earth CHECK cube")
	}
	helper, err := extension.EarthCheckHelper(view, base, n.Kind)
	if err != nil {
		return "", err
	}
	captured, err := exec.ExpressionRoutineDescriptor(n)
	if err != nil || captured == nil || captured.Address() != helper.Address() || !exec.SameRoutineDeclaration(helper, captured) {
		return "", fmt.Errorf("stuntgres: foreign earth CHECK helper capture")
	}
	namespace, ok := view.Namespace(helper.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing earth CHECK helper namespace")
	}
	function := catalog.DependencyIdentifier(namespace.Name()) + "." + catalog.DependencyIdentifier(helper.Name())
	switch n.Kind {
	case sqltype.EarthNotPoint:
		return function + "(VALUE)", nil
	case sqltype.EarthNot3D:
		return "(" + function + "(VALUE) <= 3)", nil
	default:
		ns, ok := view.Namespace(base.Namespace())
		if !ok {
			return "", fmt.Errorf("stuntgres: missing earth CHECK namespace")
		}
		name := catalog.DependencyIdentifier(ns.Name()) + "." + catalog.DependencyIdentifier(base.Name())
		return "(abs(" + function + "(VALUE, '(0)'::" + name + ") / 6378168::double precision - 1::double precision) < '10e-7'::double precision)", nil
	}
}

func (r ScalarRenderer) earthSQL(ctx context.Context, view catalog.Read, n plan.EarthCheckExpr) (string, error) {
	if r.DomainValue == nil || *r.DomainValue != n.Value.Usage {
		return "", fmt.Errorf("stuntgres: foreign earth CHECK VALUE")
	}
	if _, err := r.SQL(ctx, view, n.Value); err != nil {
		return "", err
	}
	return earthCheckSQL(view, n)
}
