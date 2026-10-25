package schemacmds

// Exported names of schemacmds internals for the external tests in this
// directory.

var (
	AdmitDefinitionMutation    = admitDefinitionMutation
	DomainRestrictError        = domainRestrictError
	ExtensionNamespaceOf       = extensionNamespaceOf
	StageDomainDependents      = stageDomainDependents
	StageExtensionUpdateLocked = stageExtensionUpdateLocked
	StageForeignLocked         = stageForeignLocked
	StageOf                    = stageOf
	StageSchemaDropLocked      = stageSchemaDropLocked
)

type (
	ExtensionHost = extensionHost
)
