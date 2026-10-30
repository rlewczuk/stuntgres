package schemacmds

import (
	"fmt"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// RoutineHost is the family host of the routine, operator, cast and
// operator-family decisions: the routine.DDLHost methods beyond the shared
// DDLHost here, and index.FamilyDDLHost with operator_family_host.go.
type RoutineHost struct {
	core.DDLHost
}

var (
	_ routine.DDLHost     = RoutineHost{}
	_ index.FamilyDDLHost = RoutineHost{}
)

func (h RoutineHost) RefreshTransformConsumers(source catalog.Read,pair *catalog.Transform)([]catalog.Descriptor,error) {
	return stored.RefreshTransformRoutines(source,h.StageRead(),pair,h.Revision)
}

func (h RoutineHost) DefinitionType(a ddl.Authority, id sqltype.ID) (*sqltype.Descriptor, bool) {
	return core.DDLAuthorityOf(a).DefinitionType(id)
}

func (h RoutineHost) DefinitionLookup(a ddl.Authority, key ids.NameKey) ids.ObjectAddress {
	return core.DDLAuthorityOf(a).DefinitionLookup(key)
}

func (h RoutineHost) RoutineSignatureSpelling(a ddl.Authority, id ids.OID) (string, error) {
	path, err := a.ReferencePath()
	if err != nil {
		return "", err
	}
	return regobj.RoutineSpelling(h.AccessView(a).Catalog, path, id, true, false), nil
}

func (h RoutineHost) SelectAggregateSupport(a ddl.Authority, name parser.ObjectName, inputs []sqltype.Usage) (*catalog.Procedure, []sqltype.Usage, sqltype.Usage, error) {
	v := core.DDLAuthorityOf(a)
	// Catalog support lookup is not a parser call transformation. Preserve
	// written names, but do not attribute namespace failures to call positions.
	lookupName := name
	var localParts [3]parser.Identifier
	lookupName.Parts = localParts[:0]
	for _, part := range name.Parts {
		part.Pos = parser.Position{}
		lookupName.Parts = append(lookupName.Parts, part)
	}
	path, err := h.E.LookupNamespaces(v, lookupName, analyze.FunctionLookup)
	if err != nil {
		return nil, nil, sqltype.Usage{}, err
	}
	reader := core.StageDefinitions(h.CatalogStage)
	previous := h.Op.Definition
	h.Op.Definition = reader
	defer func() { h.Op.Definition = previous }()
	v.Definition = reader
	binder := core.NewExpressionBinder(h.Context(), h.E, sqltype.InputEnvironment{}, h.Op, nil)
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

// Seed validation first captures the destination's diagnostics and authority.
// Domain seed input borrows the captured binder-literal unlock/readmit boundary.
// The DDL caller owns Engine.Mu, so direct scope CHECK evaluation would deadlock.
func (h RoutineHost) ValidateAggregateInitial(a ddl.Authority, usage sqltype.Usage, text string, input sqltype.InputEnvironment) error {
	auth := core.DDLAuthorityOf(a)
	if err := h.E.CaptureTypeDiagnosticInput(auth, h.Op, &input, usage.Type); err != nil {
		return err
	}
	if !usage.Type.IsDomain() {
		_, err := usage.Type.InputWithEnvironment(h.Context(), text, usage.Typmod, input)
		return err
	}
	_, err := h.E.AnalysisHost(h.Op).DecodeDomainLiteral(h.Context(), core.AuthorityOf(auth), auth.Catalog, usage.Type, text, usage.Typmod, input)
	return err
}

func (h RoutineHost) ResolveRegtype(a ddl.Authority, raw parser.TypeName) (ids.OID, error) {
	input, err := core.CaptureRegtypeInput(core.DDLAuthorityOf(a), h.Op)
	if err != nil {
		return 0, err
	}
	id, err := input.ResolveParsedTypeName(h.Op.Context(), raw, h.Op)
	return ids.OID(id), err
}

// BindSQLRoutine uses the declaration's scoped validator policy. Private replay
// and checked-PL leases retain their independent admission rules.
func (h RoutineHost) BindSQLRoutine(a ddl.Authority, d *catalog.Procedure, input sqltype.InputEnvironment, validateBody bool) error {
	if !d.SQL.Standard && d.SQL.Kind != proc.PLpgSQLBody && (!validateBody || core.OperationReplayOrZero(h.Op).SQLBodies) {
		return nil
	}
	previous := h.Op.Definition
	h.Op.Definition = core.StageDefinitions(h.CatalogStage)
	defer func() { h.Op.Definition = previous }()
	auth := core.DDLAuthorityOf(a)
	auth.Definition = h.Op.Definition
	if d.SQL.Kind == proc.PLpgSQLBody {
		language, ok := auth.Catalog.Get(d.Language).(*catalog.Language)
		if !ok {
			return fmt.Errorf("stuntgres: missing routine language")
		}
		if capture := core.OperationReplayOrZero(h.Op).PLChecked; capture != nil {
			s := capture.Statement
			if s == nil || capture.Consumed || d.SQL.Position != s.BodyPos || d.SQL.Source != s.Body || d.Name() != s.Name.Parts[len(s.Name.Parts)-1].Text || language.Validator != (ids.ObjectAddress{}) {
				return fmt.Errorf("stuntgres: mismatched checked PL replay lease")
			}
			if _, err := auth.Roles.Descriptor(capture.Owner); err != nil {
				return err
			}
			auth.State.CurrentUser = capture.Owner
			if err := h.E.CompilePLRoutineLocked(h.Op, auth, d); err != nil {
				return err
			}
			capture.Consumed = true
			return nil
		}
		if language.Validator == (ids.ObjectAddress{}) || !validateBody && !core.OperationReplayOrZero(h.Op).SQLBodies {
			return nil
		}
		return h.E.CompilePLRoutineLocked(h.Op, auth, d)
	}
	_, _, err := BindSQLRoutineLocked(h.E, h.Op, auth, d, input, validateBody)
	return err
}

// Defaults never share quoted-body replay deferral. Their legitimate stage
// includes prior staged objects, but not the declaration currently being built.
func (h RoutineHost) BindSQLDefaults(a ddl.Authority, d *catalog.Procedure, raw []parser.RoutineParameter, input sqltype.InputEnvironment) error {
	if d.RoutineDefinition().DefaultCount() == 0 {
		return nil
	}
	previous := h.Op.Definition
	h.Op.Definition = core.StageDefinitions(h.CatalogStage)
	defer func() { h.Op.Definition = previous }()
	auth := core.DDLAuthorityOf(a)
	auth.Definition = h.Op.Definition
	b := core.NewExpressionBinder(h.Op.Context(), h.E, input, h.Op, nil)
	b.Query.Auth = &auth
	b.Params.NoParameters = true
	entries := make([]expr.Expr, len(d.RoutineDefinition().Inputs))
	for formal, syntax := range raw {
		if syntax.Default == nil {
			continue
		}
		slot := d.RoutineDefinition().FormalInputs[formal]
		node, err := b.BindRoutineDefault(syntax.Default, d.RoutineDefinition().Inputs[slot])
		if err != nil {
			return err
		}
		entries[slot] = node
	}
	defaults, err := stored.NewRoutineDefaults(h.Op.Context(), auth.Catalog, b.DefinitionReader(), entries)
	if err != nil {
		return err
	}
	d.SQLDefaults = defaults
	return defaults.ValidateDefinition(d)
}
