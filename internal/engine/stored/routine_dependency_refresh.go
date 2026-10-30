package stored

import "stuntgres/internal/engine/catalog"

// dependencyRefresh is nil for ordinary import/type operations. Transform
// replacement supplies an exact original-address/revision policy; callers pass
// only their owned, already validated and copied manifest, never source backing.
type dependencyRefresh func(catalog.ExpressionDependency) (catalog.ExpressionDependency, error)

func (refresh dependencyRefresh) apply(dependencies []catalog.ExpressionDependency) error {
	if refresh == nil { return nil }
	for i := range dependencies {
		var err error
		dependencies[i], err = refresh(dependencies[i])
		if err != nil { return err }
	}
	return nil
}
