package engine

import (
	"context"
	"database/sql/driver"
)

// ForeignObjectKind is the PostgreSQL catalog relation OID passed to validators.
type ForeignObjectKind uint32

const (
	ForeignAttribute   ForeignObjectKind = 1249
	ForeignWrapper     ForeignObjectKind = 2328
	ForeignServer      ForeignObjectKind = 1417
	ForeignUserMapping ForeignObjectKind = 1418
	ForeignTable       ForeignObjectKind = 3118
)

type ForeignFunctionName struct{ Schema, Name string }
type ForeignDataWrapperDefinition struct {
	HandlerName ForeignFunctionName
	Wrapper     ForeignDataWrapper
}
type ForeignValidatorDefinition struct {
	Name     ForeignFunctionName
	Validate func(context.Context, ForeignObjectKind, []ForeignOption) error
}

// Requests own copies of their metadata slices. Returned row cells/buffers must
// stay stable until the next callback on that scan; the engine owns them by then.
type ForeignColumn struct {
	Attribute      int32
	Name, TypeName string
	TypeOID        uint32
	TypeModifier   int32
	CollationOID   uint32
	Nullable       bool
	Options        []ForeignOption
}
type ForeignScanParameters struct {
	Values      []driver.Value
	Correlation []driver.Value
}
type ForeignRequest struct {
	Schema, Table, Role                                         string
	TableOID, RoleOID                                           uint32
	Columns                                                     []ForeignColumn
	RequiredAttributes                                          []int32
	WrapperOptions, ServerOptions, TableOptions, MappingOptions []ForeignOption
	Parameters                                                  ForeignScanParameters
}
type ForeignRelSize struct{ Rows, Width float64 }
type ForeignPath struct {
	StartupCost, TotalCost float64
	Attributes             []int32
	Private                any
}
type ForeignPlan struct {
	Attributes []int32
	Private    any
}
type ForeignRow []driver.Value

type ForeignDataWrapper interface {
	GetForeignRelSize(context.Context, ForeignRequest) (ForeignRelSize, error)
	GetForeignPaths(context.Context, ForeignRequest, ForeignRelSize) ([]ForeignPath, error)
	GetForeignPlan(context.Context, ForeignRequest, ForeignPath) (ForeignPlan, error)
	BeginForeignScan(context.Context, ForeignRequest, ForeignPlan) (ForeignScan, error)
}

// A nonnil Begin result transfers cleanup ownership even alongside an error.
// Ordinary execution Ends before publishing its finalized result. A SQL cursor
// instead owns its scan across finite eager FETCH/MOVE results until terminal
// cleanup (or successful HOLD promotion). Each callback receives the current
// request context; End detaches cancellation and runs exactly once.
type ForeignScan interface {
	IterateForeignScan(context.Context) (ForeignRow, bool, error)
	ReScanForeignScan(context.Context, ForeignScanParameters) error
	EndForeignScan(context.Context) error
}

type ForeignImportRequest struct {
	RemoteSchema, LocalSchema, Server, Role                string
	WrapperOptions, ServerOptions, MappingOptions, Options []ForeignOption
	Tables                                                 []string
	Except                                                 bool
}
type ForeignColumnDefinition struct {
	Name, Type string
	Options    []ForeignOption
	NotNull    bool
	Default    string
	Check      string
}
type ForeignTableDefinition struct {
	Name    string
	Columns []ForeignColumnDefinition
	Options []ForeignOption
	Checks  []string
}
type ForeignSchemaImporter interface {
	ImportForeignSchema(context.Context, ForeignImportRequest) ([]ForeignTableDefinition, error)
}
