package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// The routine.DDLHost methods of ddlHost beyond the shared ddl.Host.

var _ routine.DDLHost = ddlHost{}

func (h ddlHost) DefinitionType(a ddl.Authority, id sqltype.ID) (*sqltype.Descriptor, bool) {
	return ddlAuthorityOf(a).definitionType(id)
}

func (h ddlHost) DefinitionLookup(a ddl.Authority, key ids.NameKey) ids.ObjectAddress {
	return ddlAuthorityOf(a).definitionLookup(key)
}

func (h ddlHost) SelectFunction(a ddl.Authority, name parser.ObjectName, signature catalog.ProcedureSignature) (catalog.RoutineIdentity, int, error) {
	v := ddlAuthorityOf(a)
	path, err := h.e.lookupNamespaces(v, name, analyze.FunctionLookup)
	if err != nil {
		return catalog.RoutineIdentity{}, 0, err
	}
	identity, count := regobj.SelectRoutine(v.Catalog, path, name.Parts[len(name.Parts)-1].Text, &signature)
	return identity, count, nil
}

func (h ddlHost) ResolveRegtype(a ddl.Authority, raw parser.TypeName) (ids.OID, error) {
	input, err := h.e.captureRegtypeInput(ddlAuthorityOf(a), h.op)
	if err != nil {
		return 0, err
	}
	id, err := input.ResolveParsedTypeName(h.op.Context(), raw, h.op)
	return ids.OID(id), err
}

// BindSQLRoutine binds d's body with the stage's definitions in place of the
// operation's. Restore defers SQL body validation until the import ends.
func (h ddlHost) BindSQLRoutine(a ddl.Authority, d *catalog.Procedure, input sqltype.InputEnvironment) error {
	if operationReplayOrZero(h.op).sqlBodies {
		return nil
	}
	previous := h.op.Definition
	h.op.Definition = stageDefinitionReader{h.stage}
	defer func() { h.op.Definition = previous }()
	auth := ddlAuthorityOf(a)
	auth.definition = h.op.Definition
	_, _, err := h.e.bindSQLRoutineLocked(h.op, auth, d, input, nil)
	return err
}
