package core

import "stuntgres/internal/engine/ids"

func SepgsqlRetirementCompletion(scope *StatementScope, address ids.ObjectAddress) (uint64, bool) {
	receipt, ok := scope.sepgsqlRetirements[address]
	return receipt.completion, ok
}
func ConsumeSepgsqlRetirement(provider *DropProvider, address ids.ObjectAddress) (bool, error) {
	return provider.consumeSepgsqlRetirement(address)
}
func SetSepgsqlRetirementCompletion(scope *StatementScope, value uint64) {
	scope.retirementCompletion = value
}
func ValidateSepgsqlRetirementPredecessors(scope *StatementScope) error {
	e := scope.Engine()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	return scope.validateSepgsqlObservations(e.Catalog.State)
}
