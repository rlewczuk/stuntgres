package engine

import "stuntgres/internal/engine/ids"

// Temporary aliases for identity values moved to internal/engine/ids
// (docs/ENGINE-SPLIT-ids.md step 1d removes them).
type (
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

	transactionInProgress = ids.TxInProgress
	transactionCommitted  = ids.TxCommitted
	transactionAborted    = ids.TxAborted
)
