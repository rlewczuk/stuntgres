package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
)

type ForeignDataWrapperDefinition struct {
	HandlerName catalog.ForeignFunctionName
	Wrapper     catalog.ForeignDataWrapper
}
type ForeignValidatorDefinition struct {
	Name     catalog.ForeignFunctionName
	Validate func(context.Context, catalog.ForeignObjectKind, []catalog.ForeignOption) error
}

type ForeignImportRequest struct {
	RemoteSchema, LocalSchema, Server, Role                string
	WrapperOptions, ServerOptions, MappingOptions, Options []catalog.ForeignOption
	Tables                                                 []string
	Except                                                 bool
}
type ForeignColumnDefinition struct {
	Name, Type string
	Options    []catalog.ForeignOption
	NotNull    bool
	Default    string
	Check      string
}
type ForeignTableDefinition struct {
	Name    string
	Columns []ForeignColumnDefinition
	Options []catalog.ForeignOption
	Checks  []string
}
type ForeignSchemaImporter interface {
	ImportForeignSchema(context.Context, ForeignImportRequest) ([]ForeignTableDefinition, error)
}
