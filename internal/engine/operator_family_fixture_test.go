package engine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
	"sync"
)

var operatorFamilyUnitSeed = sync.OnceValue(func() catalog.Read { return New().CatalogRead() })

// Reduced storage fixtures still need real family members and genuine callable
// declarations. Share an immutable ordinary engine bootstrap, never a provider
// fallback or a test callback.
func operatorFamilyTestRead() catalog.Read { return operatorFamilyUnitSeed() }
func operatorFamilyFixtureState(state *catalog.State) *catalog.State {
	for address, descriptor := range operatorFamilyTestRead().Descriptors() {
		switch address.ClassID {
		case ids.OperatorFamilyClassID, ids.OperatorClassCatalogID, ids.AccessMethodOperatorClassID, ids.AccessMethodProcedureClassID, ids.ProcedureClassID, ids.OperatorClassID:
			state.SetDescriptor(address, descriptor)
			state.BindName(catalog.DescriptorNameKey(descriptor), address)
		}
	}
	return state
}
func operatorFamilyTestOrdering(usage sqltype.Usage, class sqltype.IndexClass) (sqltype.IndexComparison, error) {
	return routine.BindIndexOrdering(operatorFamilyTestRead(), usage, class)
}
func operatorFamilyTestPartitionHash(usage sqltype.Usage, class sqltype.IndexClass) (sqltype.PartitionHash, error) {
	return routine.BindPartitionHash(operatorFamilyTestRead(), usage, class)
}
func operatorFamilyTestExclusion(usage sqltype.Usage, class sqltype.IndexClass, name string) (sqltype.ExclusionOperator, error) {
	return routine.BindExclusionOperator(operatorFamilyTestRead(), usage, class, name)
}
