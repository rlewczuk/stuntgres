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
var _ ddl.SchemaHost = DDLHost{}

// StageSchemaTable analyzes the element as the schema owner with the new
// namespace first in the explicit path, as PostgreSQL does. Both changes are
// made on a copy, so the caller's authority is unchanged.
func (h DDLHost) StageSchemaTable(a ddl.Authority, namespace ids.ObjectAddress, owner authorization.PrincipalRef, table *parser.CreateTable, input sqltype.InputEnvironment) error {
	v := DDLAuthorityOf(a)
	v.State.CurrentUser = owner
	v.pathPrefix = namespace
	return h.E.SchemaTableLocked(h.Op, v, h.CatalogStage, table, namespace, owner, input, nil)
}

func (h DDLHost) StageSchemaDependents(a ddl.Authority, schemas []*catalog.Namespace, relations []*catalog.Relation, others []catalog.Descriptor, cascade bool) error {
	return h.E.stageSchemaDomainsLocked(h.Op, DDLAuthorityOf(a), h.CatalogStage, schemas, relations, others, cascade)
}

func (h DDLHost) RemoveObject(ref ids.ObjectRef, namespace bool) error {
	return h.CatalogStage.RemoveObjectLocked(ref, namespace)
}

func (h DDLHost) AddCascadeNotice(line string) {
	h.CatalogStage.SchemaCascadeNames = append(h.CatalogStage.SchemaCascadeNames, line)
}

func (h DDLHost) RelationDiagnostic(a ddl.Authority, relation *catalog.Relation) (kind, name string) {
	return catalog.RelationDiagnosticKind(relation), relationDiagnosticName(DDLAuthorityOf(a), relation)
}
