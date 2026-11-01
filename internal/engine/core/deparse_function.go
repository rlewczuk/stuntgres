package core

import (
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqltype"
)

func DeparseInquiry(h CatalogHost, c *exec.Context, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	if read, _ := h.CommentSnapshot(); read.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing deparse catalog snapshot")
	}
	switch signature {
	case functions.DeparseFunction, functions.DeparseFunctionArguments, functions.DeparseFunctionIdentityArguments, functions.DeparseFunctionResult:
		return deparseRoutine(h, c, signature, values[0], result)
	case functions.DeparsePartitionConstraint, functions.DeparsePartitionKey:
		return partition.Deparse(c, h.DeparseInputs(), signature, values, result)
	}
	return table.Deparse(c, h.DeparseInputs(), signature, values, result)
}
