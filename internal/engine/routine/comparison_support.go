package routine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/sqltype"
)

// CaptureComparisonSupports retains real installed ordering leaves from the
// existing comparison graph. Original container routines remain intrinsic.
func CaptureComparisonSupports(view catalog.Read, comparison sqltype.Comparison) (sqltype.Comparison, error) {
	if view.Owner() == nil || view.Owner().Sepgsql == nil {
		return comparison, nil
	}
	return comparison.CaptureIndexSupports(func(usage sqltype.Usage) (sqltype.IndexSupport, error) {
		class, ok, err := view.DefaultIndexClass(403, usage.Type)
		if err != nil {
			return sqltype.IndexSupport{}, err
		}
		if !ok {
			return sqltype.IndexSupport{}, fmt.Errorf("stuntgres: missing nested ordering class")
		}
		class, err = BindClassSupport(view, class)
		if err != nil {
			return sqltype.IndexSupport{}, err
		}
		kernel, err := sqltype.BindIndexOrdering(usage, class)
		if err != nil {
			return sqltype.IndexSupport{}, err
		}
		return kernel.Support(), nil
	})
}
