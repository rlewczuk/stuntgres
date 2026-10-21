package ddl

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// SchemaHost is what CREATE and DROP SCHEMA need beyond Host: staging the
// schema's table elements, the dependents of dropped schemas, removals and
// cascade notices. Its methods follow the Host lock rule.
type SchemaHost interface {
	Host

	// StageSchemaTable stages one CREATE TABLE element of CREATE SCHEMA in
	// namespace, owned by owner. It analyzes the element as owner with
	// namespace first in the path, on its own copy of auth.
	StageSchemaTable(auth Authority, namespace ids.ObjectAddress, owner authorization.PrincipalRef, table *parser.CreateTable, input sqltype.InputEnvironment) error
	// StageSchemaDependents stages the drop of schemas with their relations
	// and other dependent objects through the engine's dependency closure.
	StageSchemaDependents(auth Authority, schemas []*catalog.Namespace, relations []*catalog.Relation, others []catalog.Descriptor, cascade bool) error
	// RemoveObject stages the removal of ref; namespace marks a schema.
	RemoveObject(ref ids.ObjectRef, namespace bool) error
	// AddCascadeNotice queues one "drop cascades to" line, emitted after
	// publication.
	AddCascadeNotice(string)
	// RelationDiagnostic is the kind and qualified name of relation as
	// dependency diagnostics print them.
	RelationDiagnostic(auth Authority, relation *catalog.Relation) (kind, name string)
}
