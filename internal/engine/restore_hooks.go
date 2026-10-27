package engine

import (
	"stuntgres/internal/engine/control"
	"stuntgres/internal/parser"
)

// restoreHooks is Restore's hook table; control.Hook explains the phases.
// Each phase walks it in order, so an entry's position fixes when it runs
// relative to the others in its phase. Entries communicate only through restoreRun, so order matters: for
// example the owner directive snapshot must run before the ALTER OWNER
// admission reads it, and the inherited foreign key capture reads the
// physical constructor before the physical self foreign key step may
// replace it.
var restoreHooks = []control.Hook[*restoreRun]{
	// Line comment decoders.
	{Name: "typedef: domain replay records", Decode: decodeRestoreDomainReplay},
	{Name: "typedef: domain default snapshots", Decode: decodeRestoreDomainDefault},
	{Name: "extension: owner records", Decode: decodeRestoreExtensionOwners},
	{Name: "view: materialized snapshot records", Decode: decodeRestoreMaterializedSnapshot},
	{Name: "trigger: retained clone records", Decode: decodeRestoreTriggerClone},
	{Name: "comparison binding records", Decode: decodeRestoreComparisonContext},
	{Name: "statistics: ndistinct records", Decode: decodeRestoreNDistinct},

	// Before admission.
	{Name: "comparison binding context", Before: beforeRestoreComparisonContext},
	{Name: "routine: SQL body replay directive", Before: beforeRestoreRoutineDirective},
	{Name: "table: physical NOT NULL boundary", Before: beforeRestorePhysicalBoundary},
	{Name: "table: physical NOT NULL constructor", Before: beforeRestorePhysicalConstructor},
	{Name: "table: deferred physical primary owner", Before: beforeRestoreDeferredPhysicalOwner},
	{Name: "table: physical import index base", Before: beforeRestorePhysicalIndexBase},
	{Name: "owner directive snapshot", Before: beforeRestoreOwnerSnapshot},
	{Name: "table: staged foreign key", Before: beforeRestoreAttributeForeignKey},
	{Name: "inheritance: inherited foreign key", Before: beforeRestoreInheritedForeignKey},
	{Name: "table: physical self foreign key", Before: beforeRestorePhysicalSelfForeignKey},

	// Admission: the statement allow-list.
	{Name: "statistics: paired owner", Admit: admitRestoreStatisticsOwner},
	{Name: "ALTER OWNER", Admit: restoreAdmitChecked(admitRestoreAlterOwner)},
	{Name: "CREATE TABLE", Admit: restoreAdmitChecked(admitRestoreCreateTable)},
	{Name: "ALTER DOMAIN", Admit: control.AdmitStatement[*restoreRun](control.CheckAlterDomain)},
	{Name: "schema object DDL", Admit: control.AdmitSchemaObject[*restoreRun]},
	{Name: "operator DDL", Admit: control.AdmitOperator[*restoreRun]},
	{Name: "CREATE FUNCTION", Admit: control.AdmitKind[*restoreRun, *parser.CreateFunction]},
	{Name: "CREATE AGGREGATE", Admit: control.AdmitKind[*restoreRun, *parser.CreateAggregate]},
	{Name: "CREATE EXTENSION", Admit: control.AdmitKind[*restoreRun, *parser.CreateExtension]},
	{Name: "ALTER MATERIALIZED VIEW", Admit: control.AdmitStatement[*restoreRun](control.CheckAlterView)},
	{Name: "CREATE VIEW", Admit: control.AdmitStatement[*restoreRun](control.CheckCreateView)},
	{Name: "CREATE MATERIALIZED VIEW", Admit: control.AdmitStatement[*restoreRun](control.CheckCreateQueryTable)},
	{Name: "CREATE INDEX", Admit: control.AdmitKind[*restoreRun, *parser.CreateIndex]},
	{Name: "ALTER INDEX", Admit: control.AdmitStatement[*restoreRun](control.CheckAlterIndex)},
	{Name: "CREATE POLICY", Admit: control.AdmitKind[*restoreRun, *parser.CreatePolicy]},
	{Name: "CREATE RULE", Admit: control.AdmitKind[*restoreRun, *parser.CreateRule]},
	{Name: "CREATE TRIGGER", Admit: control.AdmitKind[*restoreRun, *parser.CreateTrigger]},
	{Name: "ALTER TRIGGER", Admit: control.AdmitStatement[*restoreRun](control.CheckAlterTrigger)},
	{Name: "ALTER TABLE", Admit: restoreAdmitChecked(admitRestoreAlterTable)},
	{Name: "ALTER TABLE OF", Admit: control.AdmitStatement[*restoreRun](control.CheckAlterTableOf)},
	{Name: "DETACH PARTITION", Admit: restoreAdmitChecked(admitRestoreDetachPartition)},
	{Name: "DROP TABLE", Admit: restoreAdmitChecked(admitRestoreDropTable)},
	{Name: "ATTACH PARTITION", Admit: control.AdmitStatement[*restoreRun](control.CheckAttachPartition)},
	{Name: "CREATE SEQUENCE", Admit: control.AdmitStatement[*restoreRun](control.CheckCreateSequence)},
	{Name: "ALTER SEQUENCE", Admit: restoreAdmitChecked(admitRestoreAlterSequence)},
	{Name: "ALTER TABLE DEFAULT", Admit: control.AdmitStatement[*restoreRun](control.CheckAlterTableDefault)},
	{Name: "SELECT", Admit: control.AdmitStatement[*restoreRun](control.CheckSelect)},
	{Name: "COMMENT", Admit: control.AdmitStatement[*restoreRun](control.CheckComment)},
	{Name: "SET SESSION AUTHORIZATION", Admit: restoreAdmitChecked(admitRestoreSessionAuthorization)},
	{Name: "UPDATE catalog repair", Admit: restoreAdmitReplayed(replayRestoreCatalogUpdate)},
	{Name: "DELETE physical NOT NULL repair", Admit: restoreAdmitReplayed(replayRestorePhysicalNotNull)},
	{Name: "INSERT", Admit: restoreAdmitChecked(admitRestoreInsert)},
	{Name: "SQL/MED", Admit: restoreAdmitForeignDDL},

	// After admission, before execution.
	{Name: "inheritance: generation recipe data", Prepare: prepareRestoreRecipeData},
	{Name: "table: NOT NULL pair reset", Prepare: prepareRestoreNotNullPairReset},
	{Name: "table: NOT NULL pair", Prepare: prepareRestoreNotNullPair},
	{Name: "inheritance: generation pair", Prepare: prepareRestoreGenerationPair},
	{Name: "replay flags", Prepare: prepareRestoreReplayFlags},
	{Name: "partition: CHECK replay", Prepare: prepareRestorePartitionCheck},
	{Name: "partition: USING INDEX replay", Prepare: prepareRestorePartitionUsingIndex},
	{Name: "partition: UNIQUE replay", Prepare: prepareRestorePartitionUnique},

	// After execution.
	{Name: "comparison bound definition", After: afterRestoreComparisonContext},
	{Name: "table: attribute constructor capture", After: afterRestoreAttributeConstructor},
	{Name: "table: physical NOT NULL constructor capture", After: afterRestorePhysicalConstructor},
	{Name: "table: physical foreign key provider", After: afterRestorePhysicalForeignKeyProvider},
	{Name: "table: physical import index", After: afterRestorePhysicalImportIndex},
	{Name: "owner directive", After: afterRestoreOwnerDirective},
	{Name: "typedef: fresh type owner", After: afterRestoreTypeOwner},
	{Name: "statistics: fresh owner", After: afterRestoreStatisticsOwner},
	{Name: "inheritance: historical relation", After: afterRestoreHistoricalRelation},
	{Name: "table: NOT NULL origins", After: afterRestoreNotNullOrigins},
	{Name: "inheritance: generation placeholders", After: afterRestoreGenerationPlaceholders},
	{Name: "inheritance: inherited owner", After: afterRestoreInheritedOwner},
	{Name: "inheritance: historical facts", After: afterRestoreHistoricalFacts},
	{Name: "table: foreign key targets", After: afterRestoreForeignKeys},
	{Name: "table: deferred default", After: afterRestoreDefault},
	{Name: "inheritance: generation capture", After: afterRestoreGeneration},
	{Name: "table: attribute definition capture", After: afterRestoreAttributeDefinition},
	{Name: "trigger: retained clone capture", After: afterRestoreTriggerClone},

	// After the last statement.
	{Name: "comparison context closure", Finish: finishRestoreComparisonContext},
	{Name: "table: attribute retirements", Finish: finishRestoreAttributeRetirements},
	{Name: "table: physical NOT NULL boundary at end", Finish: finishRestorePhysicalBoundary},
	{Name: "table: foreign keys", Finish: finishRestoreForeignKeys},
	{Name: "extension: owners", Finish: finishRestoreExtensionOwners},
	{Name: "view: materialized snapshots", Finish: finishRestoreMaterializedSnapshots},
	{Name: "routine: SQL bodies", Finish: finishRestoreSQLRoutines},
	{Name: "trigger: retained clone links", Finish: finishRestoreTriggerClones},
	{Name: "statistics: ndistinct facts", Finish: finishRestoreNDistinct},
}
