package fdw

import (
	"context"
	"stuntgres/internal/engine/catalog"
)

type WrapperDefinition struct {
	HandlerName catalog.ForeignFunctionName
	Wrapper     catalog.ForeignDataWrapper
}
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
