package schemacmds

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/tablecmds"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// SchemaHost is the family host of CREATE SCHEMA and DROP SCHEMA: it
// implements ddl.SchemaHost over the shared DDLHost, which also supplies
// RemoveObject and RelationDiagnostic.
type SchemaHost struct {
	core.DDLHost
}

var _ ddl.SchemaHost = SchemaHost{}

// StageSchemaTable analyzes the element as the schema owner with the new
// namespace first in the explicit path, as PostgreSQL does. Both changes are
// made on a copy, so the caller's authority is unchanged.
func (h SchemaHost) StageSchemaTable(a ddl.Authority, namespace ids.ObjectAddress, owner authorization.PrincipalRef, table *parser.CreateTable, input sqltype.InputEnvironment) error {
	v := core.DDLAuthorityOf(a)
	v.State.CurrentUser = owner
	v.PathPrefix = namespace
	return tablecmds.SchemaTableLocked(h.E, h.Op, v, h.CatalogStage, table, namespace, owner, input, nil)
}

func (h SchemaHost) StageSchemaDependents(a ddl.Authority, schemas []*catalog.Namespace, relations []*catalog.Relation, others []catalog.Descriptor, cascade bool) error {
	return stageSchemaDomainsLocked(h.E, h.Op, core.DDLAuthorityOf(a), h.CatalogStage, schemas, relations, others, cascade)
}

func (h SchemaHost) AddCascadeNotice(line string) {
	h.CatalogStage.SchemaCascadeNames = append(h.CatalogStage.SchemaCascadeNames, line)
}
