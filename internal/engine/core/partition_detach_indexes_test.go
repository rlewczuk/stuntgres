package core_test

import (
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

func operatorFamilyTestOrdering(usage sqltype.Usage, class sqltype.IndexClass) (sqltype.IndexComparison, error) {
	return routine.BindIndexOrdering(operatorFamilyTestRead(), usage, class)
}
