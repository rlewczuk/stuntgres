package catalog

import (
	"context"
)

const ForeignHandlerSignature ProcedureSignature = ""

const ForeignValidatorSignature ProcedureSignature = "1009,26"

type ForeignProcedure struct {
	Name      ForeignFunctionName
	Wrapper   ForeignDataWrapper
	Validator func(context.Context, ForeignObjectKind, []ForeignOption) error
}
