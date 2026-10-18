package engine

import (
	"context"
)

type ForeignDataWrapperDefinition struct {
	HandlerName ForeignFunctionName
	Wrapper     ForeignDataWrapper
}
type ForeignValidatorDefinition struct {
	Name     ForeignFunctionName
	Validate func(context.Context, ForeignObjectKind, []ForeignOption) error
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
