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

	// Before admission.
	{Name: "routine: SQL body replay directive", Before: beforeRestoreRoutineDirective},
	{Name: "table: physical NOT NULL boundary", Before: beforeRestorePhysicalBoundary},
	{Name: "table: physical NOT NULL constructor", Before: beforeRestorePhysicalConstructor},
	{Name: "table: deferred physical primary owner", Before: beforeRestoreDeferredPhysicalOwner},
	{Name: "table: physical import index base", Before: beforeRestorePhysicalIndexBase},
	{Name: "owner directive snapshot", Before: beforeRestoreOwnerSnapshot},
	{Name: "inheritance: inherited foreign key", Before: beforeRestoreInheritedForeignKey},
	{Name: "table: physical self foreign key", Before: beforeRestorePhysicalSelfForeignKey},

	// Admission: the statement allow-list.
	{Name: "ALTER OWNER", Admit: restoreAdmitChecked(admitRestoreAlterOwner)},
	{Name: "CREATE TABLE", Admit: restoreAdmitChecked(admitRestoreCreateTable)},
	{Name: "ALTER DOMAIN", Admit: restoreAdmitChecked(admitRestoreAlterDomain)},
	{Name: "schema object DDL", Admit: restoreAdmitSchemaObject},
	{Name: "operator DDL", Admit: restoreAdmitOperator},
	{Name: "CREATE FUNCTION", Admit: restoreAdmitKind[*parser.CreateFunction]},
	{Name: "CREATE EXTENSION", Admit: restoreAdmitKind[*parser.CreateExtension]},
	{Name: "ALTER MATERIALIZED VIEW", Admit: restoreAdmitChecked(admitRestoreAlterView)},
	{Name: "CREATE VIEW", Admit: restoreAdmitChecked(admitRestoreCreateView)},
	{Name: "CREATE MATERIALIZED VIEW", Admit: restoreAdmitChecked(admitRestoreCreateQueryTable)},
	{Name: "CREATE INDEX", Admit: restoreAdmitKind[*parser.CreateIndex]},
	{Name: "ALTER INDEX", Admit: restoreAdmitChecked(admitRestoreAlterIndex)},
	{Name: "CREATE POLICY", Admit: restoreAdmitKind[*parser.CreatePolicy]},
	{Name: "CREATE RULE", Admit: restoreAdmitKind[*parser.CreateRule]},
	{Name: "ALTER TABLE", Admit: restoreAdmitChecked(admitRestoreAlterTable)},
	{Name: "ALTER TABLE OF", Admit: restoreAdmitChecked(admitRestoreAlterTableOf)},
	{Name: "DETACH PARTITION", Admit: restoreAdmitChecked(admitRestoreDetachPartition)},
	{Name: "DROP TABLE", Admit: restoreAdmitChecked(admitRestoreDropTable)},
	{Name: "ATTACH PARTITION", Admit: restoreAdmitChecked(admitRestoreAttachPartition)},
	{Name: "CREATE SEQUENCE", Admit: restoreAdmitChecked(admitRestoreCreateSequence)},
	{Name: "ALTER SEQUENCE", Admit: restoreAdmitChecked(admitRestoreAlterSequence)},
	{Name: "ALTER TABLE DEFAULT", Admit: restoreAdmitChecked(admitRestoreAlterTableDefault)},
	{Name: "SELECT", Admit: restoreAdmitChecked(admitRestoreSelect)},
	{Name: "COMMENT", Admit: restoreAdmitChecked(admitRestoreComment)},
	{Name: "SET SESSION AUTHORIZATION", Admit: restoreAdmitChecked(admitRestoreSessionAuthorization)},
	{Name: "UPDATE catalog repair", Admit: restoreAdmitReplayed(replayRestoreCatalogUpdate)},
	{Name: "DELETE physical NOT NULL repair", Admit: restoreAdmitReplayed(replayRestorePhysicalNotNull)},
	{Name: "INSERT", Admit: restoreAdmitChecked(admitRestoreInsert)},
	{Name: "SQL/MED", Admit: restoreRejectForeignDDL},

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
	{Name: "table: physical NOT NULL constructor capture", After: afterRestorePhysicalConstructor},
	{Name: "table: physical foreign key provider", After: afterRestorePhysicalForeignKeyProvider},
	{Name: "table: physical import index", After: afterRestorePhysicalImportIndex},
	{Name: "owner directive", After: afterRestoreOwnerDirective},
	{Name: "typedef: domain owner", After: afterRestoreDomainOwner},
	{Name: "inheritance: historical relation", After: afterRestoreHistoricalRelation},
	{Name: "table: NOT NULL origins", After: afterRestoreNotNullOrigins},
	{Name: "inheritance: generation placeholders", After: afterRestoreGenerationPlaceholders},
	{Name: "inheritance: inherited owner", After: afterRestoreInheritedOwner},
	{Name: "inheritance: historical facts", After: afterRestoreHistoricalFacts},
	{Name: "table: foreign key targets", After: afterRestoreForeignKeys},
	{Name: "table: deferred default", After: afterRestoreDefault},
	{Name: "inheritance: generation capture", After: afterRestoreGeneration},

	// After the last statement.
	{Name: "table: physical NOT NULL boundary at end", Finish: finishRestorePhysicalBoundary},
	{Name: "table: foreign keys", Finish: finishRestoreForeignKeys},
	{Name: "extension: owners", Finish: finishRestoreExtensionOwners},
	{Name: "view: materialized snapshots", Finish: finishRestoreMaterializedSnapshots},
	{Name: "routine: SQL bodies", Finish: finishRestoreSQLRoutines},
}
