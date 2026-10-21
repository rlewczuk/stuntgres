package engine

import (
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ddlHost implements fdw.Host: AddDomainBase (typedef_host.go) and
// RemoveObject (schema_host.go) are shared with the other feature hosts.
var _ fdw.Host = ddlHost{}

func (h ddlHost) StageForeignTable(a ddl.Authority, s *parser.ForeignDDL, input sqltype.InputEnvironment) ([]fdw.Validation, []ids.ObjectAddress, error) {
	return h.e.stageForeignTableLocked(h.op, input, ddlAuthorityOf(a), h.stage, s)
}
