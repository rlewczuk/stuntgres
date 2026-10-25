package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
)

// StageDefinitionReader names stageDefinitionReader.
type StageDefinitionReader = stageDefinitionReader

// BuiltinSeed returns builtinSeed.
func BuiltinSeed() *catalog.SeedShare { return builtinSeed }

// BuiltinSeedRef returns &builtinSeed.
func BuiltinSeedRef() **catalog.SeedShare { return &builtinSeed }

// DomainRestrictError calls domainRestrictError.
func DomainRestrictError(stage *CatalogStage, targets []ids.ObjectAddress, edges []table.DomainDependency, originalRootCount int, subject ids.ObjectAddress) error {
	return domainRestrictError(stage, targets, edges, originalRootCount, subject)
}

// DropProviderLockInheritanceClosure calls x.lockInheritanceClosure.
func DropProviderLockInheritanceClosure(x *DropProvider, roots []ids.ObjectAddress) error {
	return x.lockInheritanceClosure(roots)
}

// EngineBootstrapAuthorization calls x.bootstrapAuthorization.
func EngineBootstrapAuthorization(x *Engine, user authorization.PrincipalRef) AuthorizationView {
	return x.bootstrapAuthorization(user)
}

// EngineLookupNamespaces calls x.lookupNamespaces.
func EngineLookupNamespaces(x *Engine, auth AuthorizationView, name parser.ObjectName, kind analyze.LookupKind) (analyze.NamespaceSelection, error) {
	return x.lookupNamespaces(auth, name, kind)
}

// ScopeSchemaInquiryPathLocked calls x.schemaInquiryPathLocked.
func ScopeSchemaInquiryPathLocked(x *StatementScope, e *Engine) (AuthorizationView, []ids.ObjectAddress, error) {
	return x.schemaInquiryPathLocked(e)
}

// StageDefinitionReaderStage returns x.stage.
func StageDefinitionReaderStage(x *stageDefinitionReader) *CatalogStage { return x.stage }
