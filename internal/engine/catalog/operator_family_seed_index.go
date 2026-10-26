package catalog

import "stuntgres/internal/engine/ids"

// Bootstrap membership indexes are immutable. They identify original release
// rows only; runtime class/member selection still uses the captured catalog.
var builtinFamilyNamesByOID = func() map[ids.OID]string {
	index := make(map[ids.OID]string, len(builtinFamilyRows))
	for _, row := range builtinFamilyRows {
		index[row.oid] = row.name
	}
	return index
}()

var builtinFamilyOperatorsByOID = func() map[ids.OID]*OperatorFamilyOperator {
	index := make(map[ids.OID]*OperatorFamilyOperator, len(builtinFamilyOperators))
	for i := range builtinFamilyOperators {
		row := &builtinFamilyOperators[i]
		index[row.Address().ObjectID] = row
	}
	return index
}()

var builtinFamilySupportsByOID = func() map[ids.OID]*OperatorFamilySupport {
	index := make(map[ids.OID]*OperatorFamilySupport, len(builtinFamilySupports))
	for i := range builtinFamilySupports {
		row := &builtinFamilySupports[i]
		index[row.Address().ObjectID] = row
	}
	return index
}()
