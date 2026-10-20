package engine

import (
	"sort"
	"stuntgres/internal/engine/ids"
)

type catalogSourceRegistration struct {
	oid     ids.OID
	produce catalogSourceProducer
}

// Process-initialized, sorted by relation OID and never mutated. Registration
// order of descriptors is independent of lookup order here.
var catalogSourceProducers = [...]catalogSourceRegistration{
	{826, capturePGDefaultACL},
	{1213, capturePGTablespace},
	{1247, capturePGType},
	{1249, capturePGAttribute},
	{1255, captureExtensionCatalog},
	{1259, capturePGClass},
	{1262, capturePGDatabase},
	{1417, captureForeignCatalog},
	{1418, captureForeignCatalog},
	{2224, capturePGSequence},
	{2328, captureForeignCatalog},
	{2396, capturePGShDescription},
	{2600, capturePGAggregate},
	{2601, capturePGAM},
	{2604, capturePGAttrdef},
	{2605, capturePGCast},
	{2606, capturePGConstraint},
	{2607, capturePGConversion},
	{2608, capturePGDepend},
	{2609, capturePGDescription},
	{2610, capturePGIndex},
	{2611, capturePGInherits},
	{2612, capturePGLanguage},
	{2615, capturePGNamespace},
	{2616, capturePGOpclass},
	{2617, capturePGOperator},
	{2618, capturePGRewrite},
	{2964, capturePGDBRoleSetting},
	{3079, captureExtensionCatalog},
	{3118, captureForeignCatalog},
	{3456, capturePGCollation},
	{3501, capturePGEnum},
	{3541, capturePGRange},
	{3600, captureExtensionCatalog},
	{3764, captureExtensionCatalog},
	{6243, capturePGParameterACL},
	{12000, captureTableViewSummary},
	{12001, capturePreparedSource},
	{12002, capturePGIndexes},
	{12003, captureTableViewSummary},
	{12004, captureTimezoneSource},
	{12005, captureTimezoneSource},
	{12006, captureTableViewSummary},
	{12007, capturePGRoles},
	{12008, capturePGGroup},
	{12014, capturePGUser},
	{12077, capturePGCursors},
	{12081, captureAvailableExtensions},
	{12085, captureAvailableExtensions},
	{12095, captureForeignCatalog},
	{16200, captureTablePrivileges},
	{16201, captureDataTypePrivileges},
	{16202, capturePGSettings},
}

func catalogSourceProducerFor(oid ids.OID) (catalogSourceProducer, bool) {
	i := sort.Search(len(catalogSourceProducers), func(i int) bool { return catalogSourceProducers[i].oid >= oid })
	if i == len(catalogSourceProducers) || catalogSourceProducers[i].oid != oid {
		return nil, false
	}
	return catalogSourceProducers[i].produce, true
}
