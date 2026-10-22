package stored

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/catalog"
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
	ext, ok := view.Extension(base.Extension)
	if !ok || extension.StockCube.ValidateExtension(view, ext) != nil {
		return "", fmt.Errorf("stuntgres: missing earth CHECK cube installation")
	}
	ns, ok := view.Namespace(base.Namespace())
	if !ok {
		return "", fmt.Errorf("stuntgres: missing earth CHECK namespace")
	}
	q := catalog.DependencyIdentifier(ns.Name()) + "."
	switch n.Kind {
	case sqltype.EarthNotPoint:
		return q + "cube_is_point(VALUE)", nil
	case sqltype.EarthNot3D:
		return "(" + q + "cube_dim(VALUE) <= 3)", nil
	default:
		return "(abs(" + q + "cube_distance(VALUE, '(0)'::" + q + "cube) / 6378168::double precision - 1::double precision) < '10e-7'::double precision)", nil
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
