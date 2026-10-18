package engine

import "stuntgres/internal/engine/ids"

// Temporary aliases for identity values moved to internal/engine/ids
// (docs/ENGINE-SPLIT-ids.md step 1d removes them).
type (
	OID                = ids.OID
	CatalogEpoch       = ids.CatalogEpoch
	DefinitionRevision = ids.DefinitionRevision
	ObjectAddress      = ids.ObjectAddress
	ObjectRef          = ids.ObjectRef
	NameKey            = ids.NameKey
	ObjectKind         = ids.ObjectKind
	Domain             = ids.Domain
	SessionID          = ids.SessionID
	SessionToken       = ids.SessionToken
	SessionOwner       = ids.SessionOwner
	SessionOwnerMarker = ids.SessionOwnerMarker
	TxID               = ids.TxID
	TxRef              = ids.TxRef
	TxOwner            = ids.TxOwner
	TxOwnerMarker      = ids.TxOwnerMarker
	CommandID          = ids.CommandID
	CommitSeq          = ids.CommitSeq
	RowID              = ids.RowID
	VersionID          = ids.VersionID

	transactionIdentityState = ids.TxCompletion
)

const (
	NamespaceClassID      = ids.NamespaceClassID
	RelationClassID       = ids.RelationClassID
	TypeClassID           = ids.TypeClassID
	CollationClassID      = ids.CollationClassID
	ConversionClassID     = ids.ConversionClassID
	PolicyClassID         = ids.PolicyClassID
	ParameterClassID      = ids.ParameterClassID
	TablespaceClassID     = ids.TablespaceClassID
	ProcedureClassID      = ids.ProcedureClassID
	ExtensionClassID      = ids.ExtensionClassID
	TSTemplateClassID     = ids.TSTemplateClassID
	TSDictionaryClassID   = ids.TSDictionaryClassID
	DatabaseClassID       = ids.DatabaseClassID
	ForeignWrapperClassID = ids.ForeignWrapperClassID
	ForeignServerClassID  = ids.ForeignServerClassID
	ForeignMappingClassID = ids.ForeignMappingClassID
	ForeignTableClassID   = ids.ForeignTableClassID
	OperatorClassID       = ids.OperatorClassID
	PGCatalogNamespaceOID = ids.PGCatalogNamespaceOID
	PGToastNamespaceOID   = ids.PGToastNamespaceOID
	PublicNamespaceOID    = ids.PublicNamespaceOID

	NamespaceObject      = ids.NamespaceObject
	RelationObject       = ids.RelationObject
	TypeObject           = ids.TypeObject
	CollationObject      = ids.CollationObject
	ConversionObject     = ids.ConversionObject
	ExtensionObject      = ids.ExtensionObject
	ProcedureObject      = ids.ProcedureObject
	TSTemplateObject     = ids.TSTemplateObject
	TSDictionaryObject   = ids.TSDictionaryObject
	DatabaseObject       = ids.DatabaseObject
	OperatorObject       = ids.OperatorObject
	ForeignWrapperObject = ids.ForeignWrapperObject
	ForeignServerObject  = ids.ForeignServerObject
	ForeignMappingObject = ids.ForeignMappingObject
	ParameterObject      = ids.ParameterObject
	TablespaceObject     = ids.TablespaceObject

	transactionInProgress = ids.TxInProgress
	transactionCommitted  = ids.TxCommitted
	transactionAborted    = ids.TxAborted
)
