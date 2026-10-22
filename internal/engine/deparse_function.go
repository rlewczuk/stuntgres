package engine

import (
	"fmt"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func (h *statementHost) DeparseInquiry(c *exec.Context, signature functions.DeparseSignature, values [3]sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	scope := h.scope()
	if scope.inquiry.commentCatalog.State() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing deparse catalog snapshot")
	}
	return table.Deparse(c, scope.deparseInputs(), signature, values, result)
}

// deparseInputs reads the statement's inquiry snapshot for table.Deparse.
func (s *StatementScope) deparseInputs() table.DeparseInputs {
	auth := s.inquiry.deparseAuth
	in := table.DeparseInputs{
		Catalog:   s.inquiry.commentCatalog,
		Path:      s.inquiry.deparsePath,
		Output:    s.output,
		Temporary: func() ids.ObjectAddress { return temporaryNamespace(auth) },
		ResolveRelation: func(name parser.ObjectName) (ids.ObjectRef, error) {
			return resolveRelationSnapshot(auth, name, true)
		},
	}
	// pg_get_serial_sequence never reads the string setting; a scope without
	// an operation still answers it.
	if s.op != nil {
		in.NonstandardStrings = s.op.NonstandardStrings
	}
	if auth.session != nil {
		in.Database = auth.session.DatabaseName
	}
	return in
}
