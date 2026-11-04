package schemacmds

// Exported names of schemacmds internals for the external tests in this
// directory.

var (
	AdmitDefinitionMutation    = admitDefinitionMutation
	DomainRestrictError        = domainRestrictError
	ExtensionNamespacesOf      = extensionNamespacesOf
	StageExtensionLocked       = stageExtensionLocked
	StageExtensionUpdateLocked = stageExtensionUpdateLocked
	StageForeignLocked         = stageForeignLocked
	StageOf                    = stageOf
	PublishExtensionStage      = publishExtensionStage
	StageSchemaDropLocked      = stageSchemaDropLocked
	StageSchemaLocked          = stageSchemaLocked
)

type (
	ExtensionHost = extensionHost
)
