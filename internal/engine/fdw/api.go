package fdw

import (
	"context"
	"stuntgres/internal/engine/catalog"
)

// WrapperDefinition registers a native Go capability. Declaration display is
// GO/EXTERNAL/INVOKER with empty owned source and no external link symbol; it is
// independent of execution Language and metadata reads never call the wrapper.
type WrapperDefinition struct {
	HandlerName catalog.ForeignFunctionName
	Wrapper     catalog.ForeignDataWrapper
}

// ValidatorDefinition carries the same native Go declaration metadata contract.
type ValidatorDefinition struct {
	Name     catalog.ForeignFunctionName
	Validate func(context.Context, catalog.ForeignObjectKind, []catalog.ForeignOption) error
}

type ImportRequest struct {
	RemoteSchema, LocalSchema, Server, Role                string
	WrapperOptions, ServerOptions, MappingOptions, Options []catalog.ForeignOption
	Tables                                                 []string
	Except                                                 bool
}
type ColumnDefinition struct {
	Name, Type string
	Options    []catalog.ForeignOption
	NotNull    bool
	Default    string
	Check      string
}
type TableDefinition struct {
	Name    string
	Columns []ColumnDefinition
	Options []catalog.ForeignOption
	Checks  []string
}
type SchemaImporter interface {
	ImportForeignSchema(context.Context, ImportRequest) ([]TableDefinition, error)
}
