package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
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

func (h ddlHost) SelectAggregateSupport(a ddl.Authority, name parser.ObjectName, inputs []sqltype.Usage) (*catalog.Procedure, []sqltype.Usage, sqltype.Usage, error) {
	v := ddlAuthorityOf(a)
	path, err := h.e.lookupNamespaces(v, name, analyze.FunctionLookup)
	if err != nil {
		return nil, nil, sqltype.Usage{}, err
	}
	reader := stageDefinitionReader{h.stage}
	previous := h.op.Definition
	h.op.Definition = reader
	defer func() { h.op.Definition = previous }()
	v.definition = reader
	binder := newExpressionBinder(h.Context(), h.e, sqltype.InputEnvironment{}, h.op, nil)
	binder.Query.Auth = &v
	selected, err := binder.SelectAggregateSupport(v.Catalog, reader, path, name, inputs)
	if err != nil {
		return nil, nil, sqltype.Usage{}, err
	}
	if selected.Procedure.Builtin != nil && selected.Procedure.Builtin.ReturnsSet {
		formals := selected.Inputs
		if selected.Targets != nil {
			formals = selected.Targets
		}
		return selected.Procedure, formals, sqltype.Usage{}, nil
	}
	formals, result, err := routine.AggregateSupportTypes(v.Catalog, reader, selected.Procedure, nil, inputs)
	return selected.Procedure, formals, result, err
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
	if operationReplayOrZero(h.op).sqlBodies && !d.SQL.Standard && d.SQL.Kind != proc.PLpgSQLBody {
		return nil
	}
	previous := h.op.Definition
	h.op.Definition = stageDefinitionReader{h.stage}
	defer func() { h.op.Definition = previous }()
	auth := ddlAuthorityOf(a)
	auth.definition = h.op.Definition
	if d.SQL.Kind == proc.PLpgSQLBody {
		return h.e.compilePLRoutineLocked(h.op, auth, d)
	}
	_, _, err := h.e.bindSQLRoutineLocked(h.op, auth, d, input, nil)
	return err
}

// Defaults never share quoted-body replay deferral. Their legitimate stage
// includes prior staged objects, but not the declaration currently being built.
func (h ddlHost) BindSQLDefaults(a ddl.Authority, d *catalog.Procedure, raw []parser.RoutineParameter, input sqltype.InputEnvironment) error {
	if d.SQL.DefaultCount() == 0 {
		return nil
	}
	previous := h.op.Definition
	h.op.Definition = stageDefinitionReader{h.stage}
	defer func() { h.op.Definition = previous }()
	auth := ddlAuthorityOf(a)
	auth.definition = h.op.Definition
	b := newExpressionBinder(h.op.Context(), h.e, input, h.op, nil)
	b.Query.Auth = &auth
	b.Params.NoParameters = true
	entries := make([]expr.Expr, len(d.SQL.Inputs))
	for formal, syntax := range raw {
		if syntax.Default == nil {
			continue
		}
		slot := d.SQL.FormalInputs[formal]
		node, err := b.BindRoutineDefault(syntax.Default, d.SQL.Inputs[slot])
		if err != nil {
			return err
		}
		entries[slot] = node
	}
	defaults, err := stored.NewRoutineDefaults(h.op.Context(), auth.Catalog, b.DefinitionReader(), entries)
	if err != nil {
		return err
	}
	d.SQLDefaults = defaults
	return defaults.ValidateDefinition(d)
}
