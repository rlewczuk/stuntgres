package exec

import (
	"context"
	"time"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/fts"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// The capability groups Host returns from its accessors. Each group is one
// runtime that a later package below core may own; the Context forwarders keep
// the prologue and nil-host diagnostics. Like Host, every method is called
// without Engine.mu held.

// ContribRuntime is the contrib function runtime: amcheck, pageinspect,
// pgrowlocks, pgstattuple, pg_visibility, dblink, tablefunc and generic file functions.
type ContribRuntime interface {
	GenericFileList(functions.GenericFileListKind, string) error
	GenericFileRead(functions.GenericFileReadKind, string, int64, bool) error
	GenericFileTmpdir(uint32) error
	Pgrowlocks(name string, result *sqltype.Descriptor) ([]sqltype.Value, error)
	ValidatePgrowlocks(n plan.InstalledPgrowlocksExpr) error
	ValidatePgpArmorHeaders(n plan.InstalledPgpArmorHeadersExpr) error
	ValidateTablefuncNormal(n plan.InstalledTablefuncNormalExpr) error
	ValidateConnectby(n plan.InstalledConnectbyExpr) error
	Connectby(request functions.ConnectbyRequest) ([]sqltype.Value, error)
	TablefuncCrosstab(text string, result *sqltype.Descriptor) ([]sqltype.Value, error)
	ValidateTablefuncCrosstab(n plan.InstalledTablefuncCrosstabExpr) error
	VerifyHeapam(args [6]sqltype.Value, result *sqltype.Descriptor) ([]sqltype.Value, error)
	ValidateVerifyHeapam(n plan.InstalledVerifyHeapamExpr) error
	Amcheck(n plan.InstalledAmcheckExpr, oid uint32, options [3]bool) error
	PgstattupleStats(n plan.InstalledPgstattupleStatsExpr, input sqltype.Value) ([8]int64, [2]float64, error)
	Pgstattuple(n plan.InstalledPgstattupleExpr, input sqltype.Value) (storage.HeapStatistics, error)
	DblinkBuildSQLDelete(n plan.InstalledDblinkBuildSQLDeleteExpr, values [4]sqltype.Value) (string, error)
	Pgvisibility(n plan.InstalledPgvisibilityExpr, oid uint32, block int64) (storage.VisibilityMap, error)
	Pageinspect(n plan.InstalledPageinspectExpr, index uint32) ([]sqltype.BrinStoredColumn, error)
	PageinspectHeap(n plan.InstalledPageinspectExpr, relation uint32) ([]sqltype.HeapStoredColumn, error)
	PageinspectNotice(notice sqlstate.Error)
}

// TextSearchRuntime is the text-search runtime: conversion, dictionaries,
// unaccent, ts_rewrite, ts_debug, ts_stat and parser resolution.
type TextSearchRuntime interface {
	RewriteQuerySQL(query sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error)
	LexizeDictionary(dictionary sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error)
	ConvertTextSearch(ctx context.Context, kind fts.ConversionKind, explicit bool, configuration sqltype.Value, text string, result *sqltype.Descriptor) (sqltype.Value, error)
	ConvertJSONTextSearch(ctx context.Context, explicit bool, configuration, document, filter sqltype.Value, filtered bool, result *sqltype.Descriptor) (sqltype.Value, error)
	UnaccentText(procedure ids.ObjectRef, dictionary sqltype.Value, input string, result *sqltype.Descriptor) (sqltype.Value, error)
	functions.FTSInspectionRuntime
	functions.FTSParserRuntime
}

// CatalogInquiryRuntime holds the bodies of functions.CatalogInquiryRuntime:
// type, size, tablespace, partition, publication, comment, deparse, object
// address, index property and visibility inquiry.
type CatalogInquiryRuntime interface {
	FormatType(uint32, int32, bool, *sqltype.Descriptor) (sqltype.Value, error)
	BaseType(uint32, *sqltype.Descriptor) (sqltype.Value, error)
	CatalogUserName(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error)
	TablespaceLocation(oid uint32, result *sqltype.Descriptor) (sqltype.Value, error)
	TablespaceDatabase(oid uint32, result *sqltype.Descriptor) (sqltype.Value, bool, error)
	SizeInquiry(functions.SizeKind, sqltype.Value, string, *sqltype.Descriptor) (sqltype.Value, error)
	DatabaseFilesInquiry(functions.DatabaseFilesKind, sqltype.Value, sqltype.Value, *sqltype.Descriptor) (sqltype.Value, error)
	PartitionTree(uint32, *sqltype.Descriptor) (expr.SourceCursor, error)
	PartitionAncestors(uint32, *sqltype.Descriptor) (expr.SourceCursor, error)
	PartitionRoot(uint32, *sqltype.Descriptor) (sqltype.Value, error)
	RelationIsPublishable(uint32, *sqltype.Descriptor) (sqltype.Value, error)
	PublicationTables(sqltype.Value, *sqltype.Descriptor) (expr.SourceCursor, error)
	CommentInquiry(c *Context, kind functions.CommentInquiryKind, values [2]sqltype.Value, provenance expr.Info, result *sqltype.Descriptor) (sqltype.Value, error)
	DeparseInquiry(c *Context, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	DescribeObject(c *Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentifyObject(c *Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentifyObjectAddress(c *Context, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
	IndexProperty(kind functions.IndexPropertyKind, oid uint32, ordinalValue sqltype.Value, property string) (value, known bool, err error)
	LookupObjectAddress(c *Context, values [3]sqltype.Value) (ids.ObjectAddress, error)
	VisibilityInquiry(kind functions.VisibilityInquiryKind, value sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error)
}

// SessionInquiryRuntime holds the bodies of functions.SessionInquiryRuntime.
type SessionInquiryRuntime interface {
	CurrentSchemas(implicit bool, result *sqltype.Descriptor) (sqltype.Value, error)
	IdentityValue(c *Context, kind functions.IdentityKind, result *sqltype.Descriptor) (sqltype.Value, error)
	MyTempNamespace() (uint32, error)
	OtherTempNamespace(oid uint32) (bool, error)
	ListeningChannels(result *sqltype.Descriptor) ([]sqltype.Value, error)
	NotificationQueueUsage() (float64, error)
}

// TransactionInquiryRuntime holds the bodies of
// functions.TransactionInquiryRuntime.
type TransactionInquiryRuntime interface {
	CommitTimestamp(xid uint32) (time.Time, uint32, bool, error)
	CurrentTransactionID(assign bool) (uint64, error)
	CurrentSnapshot() (*sqltype.Snapshot, error)
	LastCommittedTransaction() (uint32, time.Time, uint32, bool, error)
	MultiTransactionAge(xid uint32) (int32, error)
	TransactionAge(xid uint32) (int32, error)
	TransactionMembers(xid uint32, result *sqltype.Descriptor) ([]sqltype.Value, error)
	TransactionStatus(full uint64) (string, bool, error)
}
