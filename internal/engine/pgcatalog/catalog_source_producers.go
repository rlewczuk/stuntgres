package pgcatalog

import (
	"sort"
	"stuntgres/internal/engine/ids"
)

// Registration pairs one system relation OID with its row producer.
type Registration struct {
	OID     ids.OID
	Produce Producer
}

// Process-initialized, sorted by relation OID and never mutated. Registration
// order of descriptors is independent of lookup order here.
var producers = [...]Registration{
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
	{2602, captureOperatorFamilies},
	{2603, captureOperatorFamilies},
	{2604, capturePGAttrdef},
	{2605, capturePGCast},
	{2606, capturePGConstraint},
	{2607, capturePGConversion},
	{2608, capturePGDepend},
	{2609, capturePGDescription},
	{2610, capturePGIndex},
	{2611, capturePGInherits},
	{2612, capturePGLanguage},
	{2613, capturePGLargeObject},
	{2615, capturePGNamespace},
	{2616, capturePGOpclass},
	{2617, capturePGOperator},
	{2618, capturePGRewrite},
	{2753, captureOperatorFamilies},
	{2964, capturePGDBRoleSetting},
	{2995, capturePGLargeObjectMetadata},
	{3079, captureExtensionCatalog},
	{3118, captureForeignCatalog},
	{3456, capturePGCollation},
	{3466, capturePGEventTrigger},
	{3501, capturePGEnum},
	{3541, capturePGRange},
	{TransformOID, capturePGTransform},
	{3600, capturePGTSDict},
	{TSParserOID, capturePGTSParser},
	{TSConfigOID, capturePGTSConfig},
	{3764, capturePGTSTemplate},
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
	{StatAllTablesOID, captureTableStatistics},
	{StatSysTablesOID, captureTableStatistics},
	{StatUserTablesOID, captureTableStatistics},
	{ProgressVacuumOID, captureVacuumProgress},
	{ShadowOID, capturePGShadow},
	{12014, capturePGUser},
	{RulesOID, capturePGRules},
	{12077, capturePGCursors},
	{12081, captureAvailableExtensions},
	{12085, captureAvailableExtensions},
	{12095, captureForeignCatalog},
	{WaitEventsOID, capturePGWaitEvents},
	{16200, captureTablePrivileges},
	{16201, captureDataTypePrivileges},
	{16202, capturePGSettings},
	{InfoUserMappingsOID, captureInfoUserMappings},
	{KeyColumnUsageOID, captureKeyColumnUsage},
	{InfoRoutineSequenceUsageOID, captureInfoRoutineSequenceUsage},
	{InfoCharacterSetsOID, captureInfoCharacterSets},
	{ConstraintColumnUsageOID, captureConstraintColumnUsage},
	{InfoEnabledRolesOID, captureInfoEnabledRoles},
	{InfoViewTableUsageOID, captureInfoViewTableUsage},
	{InfoColumnColumnUsageOID, captureInfoColumnColumnUsage},
	{InfoDomainConstraintsOID, captureInfoDomainConstraints},
	{InfoSequencesOID, captureInfoSequences},
	{InfoRoleColumnGrantsOID, captureInfoRoleColumnGrants},
	{InfoColumnsOID, captureInfoColumns},
	{InfoDomainsOID, captureInfoDomains},
	{InfoViewsOID, captureInfoViews},
	{InfoTablesOID, captureInfoTables},
	{InfoRoleTableGrantsOID, captureInfoRoleTableGrants},
	{InfoRoutinesOID, captureInfoRoutines},
	{InfoRoleRoutineGrantsOID, captureInfoRoleRoutineGrants},
	{InfoRoleUdtGrantsOID, captureInfoRoleUdtGrants},
	{InfoSQLFeaturesOID, captureInfoSQLFeatures},
	{InfoRoleUsageGrantsOID, captureInfoRoleUsageGrants},
	{InfoSQLSizingOID, captureInfoSQLSizing},
	{InfoSQLImplementationInfoOID, captureInfoSQLImplementationInfo},
	{InfoSQLPartsOID, captureInfoSQLParts},
	{InfoRoutinePrivilegesOID, captureInfoRoutinePrivileges},
	{InfoCheckConstraintRoutineUsageOID, captureInfoCheckConstraintRoutineUsage},
	{InfoCollationsOID, captureInfoCollations},
	{InfoSchemataOID, captureInfoSchemata},
	{InfoColumnOptionsOID, captureInfoColumnOptions},
	{InfoRoutineRoutineUsageOID, captureInfoRoutineRoutineUsage},
	{InfoUsagePrivilegesOID, captureInfoUsagePrivileges},
}

// Registrations returns a copy of the registry in lookup (OID) order.
func Registrations() []Registration {
	return append([]Registration(nil), producers[:]...)
}

// ProducerFor finds the producer registered for a system relation OID.
func ProducerFor(oid ids.OID) (Producer, bool) {
	i := sort.Search(len(producers), func(i int) bool { return producers[i].OID >= oid })
	if i == len(producers) || producers[i].OID != oid {
		return nil, false
	}
	return producers[i].Produce, true
}
