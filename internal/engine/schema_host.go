package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// ddlHost implements ddl.SchemaHost here; see ddl_host.go.
var _ ddl.SchemaHost = ddlHost{}

// StageSchemaTable analyzes the element as the schema owner with the new
// namespace first in the explicit path, as PostgreSQL does. Both changes are
// made on a copy, so the caller's authority is unchanged.
func (h ddlHost) StageSchemaTable(a ddl.Authority, namespace ids.ObjectAddress, owner authorization.PrincipalRef, table *parser.CreateTable, input sqltype.InputEnvironment) error {
	v := ddlAuthorityOf(a)
	v.State.CurrentUser = owner
	v.pathPrefix = namespace
	return h.e.schemaTableLocked(h.op, v, h.stage, table, namespace, owner, input, nil)
}

func (h ddlHost) StageSchemaDependents(a ddl.Authority, schemas []*catalog.Namespace, relations []*catalog.Relation, others []catalog.Descriptor, cascade bool) error {
	return h.e.stageSchemaDomainsLocked(h.op, ddlAuthorityOf(a), h.stage, schemas, relations, others, cascade)
}

func (h ddlHost) RemoveObject(ref ids.ObjectRef, namespace bool) error {
	return h.stage.removeObjectLocked(ref, namespace)
}

func (h ddlHost) AddCascadeNotice(line string) {
	h.stage.schemaCascadeNames = append(h.stage.schemaCascadeNames, line)
}

func (h ddlHost) RelationDiagnostic(a ddl.Authority, relation *catalog.Relation) (kind, name string) {
	return catalog.RelationDiagnosticKind(relation), relationDiagnosticName(ddlAuthorityOf(a), relation)
}
