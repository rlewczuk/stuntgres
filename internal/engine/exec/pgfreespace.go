package exec

import (
	"context"
	"fmt"
	"math"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func (c *Context) pgFreespaceArguments(n plan.InstalledPgFreespaceExpr) (values [2]sqltype.Value, err error) {
	if err := EvaluationContext(c); err != nil {
		return values, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return values, fmt.Errorf("stuntgres: invalid installed pg_freespace reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil {
		return values, err
	}
	boundary := n.PgFreespace
	boundary.RoutineReference = current
	if err := extension.ValidatePgFreespaceCall(view, current, boundary); err != nil {
		return values, err
	}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return values, err
	}
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil {
		return values, err
	}
	current, err = ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil {
		return values, err
	}
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil {
		return values, err
	}
	n.Ref.Epoch = auth.Catalog.Epoch()
	n.Procedure, n.RoutineReference = current, current
	if err := extension.ValidatePgFreespaceIdentity(auth.Catalog, current, n.Ref); err != nil {
		return values, err
	}
	for i, child := range n.Args {
		value, err := child.Eval(c)
		if err != nil {
			return values, err
		}
		values[n.Slots[i]] = value
	}
	return values, nil
}

func (c *Context) EvalPgFreespace(n plan.InstalledPgFreespaceExpr) (sqltype.Value, error) {
	if n.Kind.Set() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: pg_freespace set requires source invocation")
	}
	values, err := c.pgFreespaceArguments(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	if values[0].IsNull() || values[1].IsNull() {
		return sqltype.Null(n.Usage.Type), nil
	}
	token, err := n.EnterRoutine(c, values[:])
	if err != nil {
		return sqltype.Value{}, err
	}
	defer token.Leave()
	return c.pgFreespaceBlock(n, values[0], values[1], n.Usage.Type)
}

// pgFreespaceBlock is the actual C2 kernel, shared by explicit calls and the
// entered SQL wrapper's selected per-row child. Its caller owns the body token.
func (c *Context) pgFreespaceBlock(n plan.InstalledPgFreespaceExpr, relation, number sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	oid, err := relation.ClassOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	block, err := number.Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	// Body entry consumes the original callsite. Only the actual host boundary
	// follows its admitted declaration to the current presentation and epoch.
	auth, dependencies, err := RoutineInvocationAuthority(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil {
		return sqltype.Value{}, err
	}
	current, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil {
		return sqltype.Value{}, err
	}
	n.Ref = auth.Ref(current.Address())
	n.Procedure, n.RoutineReference = current, current
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing pg_freespace statement runtime")
	}
	layout, err := host.Contrib().PgFreespace(n, oid, block)
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Integer(result, layout.Probe(block))
}

// Only supplied values and arithmetic series state survive a yield, never an
// evaluator, token, physical session, captured permission or materialized rows.
type pgFreespaceSource struct {
	relation    sqltype.Value
	initialized bool
	empty       bool
	procedure   *catalog.Procedure
	reference   ids.ObjectRef
	occurrence  uint32
	source      expr.FoldSourceOccurrence
	// Fixed source-owned formal slots avoid boxing two literal operands on
	// every produced row. They contain values only, never borrowed authority.
	formals  [2]plan.LiteralExpr
	operands [2]expr.Expr
}

func (c *Context) OpenPgFreespace(n plan.InstalledPgFreespaceExpr) (expr.SourceCursor, error) {
	if !n.Kind.Set() {
		value, err := c.EvalPgFreespace(n)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(value), nil
	}
	values, err := c.pgFreespaceArguments(n)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if n.Procedure.Strict && values[0].IsNull() {
		return expr.OwnedSource(n.Usage.Type, nil), nil
	}
	state := &pgFreespaceSource{relation: values[0], procedure: n.Procedure, reference: n.Ref, occurrence: n.Occurrence, source: n.Source}
	state.formals[0] = plan.LiteralExpr{Info: expr.Info{Usage: values[0].Type().Usage()}, Value: values[0]}
	state.formals[1].Info.Usage = n.Usage.Type.CompositeMember(0).Usage
	state.operands = [2]expr.Expr{&state.formals[0], &state.formals[1]}
	return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, State: state, Advance: nextPgFreespace}), nil
}

func nextPgFreespace(_ context.Context, _ *expr.SourceCursor) (sqltype.Value, bool, error) {
	return sqltype.Value{}, false, fmt.Errorf("stuntgres: missing pg_freespace source authority")
}

func freespaceCall(c *Context, outer *catalog.Procedure, source expr.Info, name, signature string) (*catalog.Procedure, expr.Info, error) {
	recipe, ok := proc.RoutineDeclarationRecipe(outer.DeclarationBody)
	if !ok {
		return nil, expr.Info{}, fmt.Errorf("stuntgres: missing pg_freespace declaration")
	}
	for ordinal := range recipe.CallCount() {
		_, part := recipe.CallPart(ordinal)
		if part.Kind == proc.DeclarationRoutine && part.Name == name && part.Signature == signature ||
			part.Kind == proc.DeclarationExtensionMember && part.Slot == 0 && name == "pg_freespace" {
			return NativeDeclarationCall(c, outer, source, ordinal)
		}
	}
	return nil, expr.Info{}, fmt.Errorf("stuntgres: missing pg_freespace source call %s", name)
}

// Called only within the authentic entered size wrapper after its selected
// two-argument body descriptor has been validated by that same child evaluator.
func (c *Context) pgFreespaceExtent(n plan.InstalledPgFreespaceExpr, relation sqltype.Value, result *sqltype.Descriptor) (sqltype.Value, error) {
	auth, _, err := RoutineInvocationAuthority(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	current, err := ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil {
		return sqltype.Value{}, err
	}
	n.Ref = auth.Ref(current.Address())
	n.Procedure, n.RoutineReference = current, current
	oid, err := relation.ClassOID()
	if err != nil {
		return sqltype.Value{}, err
	}
	host := c.LiveHost()
	if host == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing pg_freespace extent runtime")
	}
	layout, present, err := host.Contrib().PgFreespaceExtent(n, oid)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !present {
		return sqltype.Null(result), nil
	}
	if layout.Blocks < 0 || layout.Blocks > math.MaxInt64/8192 {
		return sqltype.Value{}, sqlError("22003", "bigint out of range")
	}
	return sqltype.Integer(result, layout.Blocks*8192)
}

func (c *Context) freespaceSeriesStop(n plan.InstalledPgFreespaceExpr, relation sqltype.Value) (int64, bool, error) {
	size, info, err := freespaceCall(c, n.Procedure, n.Info, "pg_relation_size", "2205")
	if err != nil {
		return 0, false, err
	}
	token, err := info.EnterRoutine(c, []sqltype.Value{relation})
	if err != nil {
		return 0, false, err
	}
	var bytes sqltype.Value
	if relation.IsNull() {
		bytes = sqltype.Null(n.Usage.Type.CompositeMember(0).Usage.Type)
	} else {
		// pg_relation_size(regclass) is itself a real SQL wrapper. Its entered
		// child context consumes the original two-argument body's selected ref.
		child, failure := BeginNativeBody(c, size, info)
		if failure == nil {
			auth, deps, failure := RoutineInvocationAuthority(&child)
			if failure == nil {
				if len(size.Body) != 1 {
					failure = fmt.Errorf("stuntgres: invalid pg_relation_size body")
				} else if _, failure = BodyRoutineDescriptor(&child, auth, deps, size.Body[0]); failure == nil {
					// This wrapper's technical extent uses its existing
					// snapshot-visible freespace layout, not the unrelated
					// global logical allocation counter. The real selected
					// INTERNAL child remains fmgrtab-exempt.
					bytes, failure = child.pgFreespaceExtent(n, relation, n.Usage.Type.CompositeMember(0).Usage.Type)
				}
			}
			if failure != nil {
				token.Leave()
				return 0, false, nativeDeclarationStatementContext(c, size, failure)
			}
		}
		if failure != nil {
			token.Leave()
			return 0, false, nativeDeclarationStatementContext(c, size, failure)
		}
	}
	token.Leave()
	_, info, err = freespaceCall(c, n.Procedure, n.Info, "current_setting", "25")
	if err != nil {
		return 0, false, err
	}
	text, ok := c.Catalog.LookupType(sqltype.Text)
	if !ok {
		return 0, false, fmt.Errorf("stuntgres: missing current_setting text type")
	}
	settings := [1]sqltype.Value{sqltype.String(text, "block_size")}
	token, err = info.EnterRoutine(c, settings[:])
	if err != nil {
		return 0, false, err
	}
	value, err := c.InvokeSetting("current_setting", settings[:], text)
	token.Leave()
	if err != nil {
		return 0, false, err
	}
	raw, err := value.Text()
	if err != nil {
		return 0, false, err
	}
	integer := n.Usage.Type.CompositeMember(0).Usage.Type
	width, err := integer.InputWithEnvironment(c.Context, raw, -1, c.FunctionInput())
	if err != nil {
		return 0, false, err
	}
	divide, err := sqltype.BindArithmetic(sqltype.ArithmeticDivide, integer, integer, integer)
	if err != nil {
		return 0, false, err
	}
	blocks, err := divide.Binary(c.Context, bytes, width, c.FunctionInput())
	if err != nil {
		return 0, false, err
	}
	if blocks.IsNull() {
		return 0, true, nil
	}
	one, err := sqltype.Integer(integer, 1)
	if err != nil {
		return 0, false, err
	}
	subtract, err := sqltype.BindArithmetic(sqltype.ArithmeticSubtract, integer, integer, integer)
	if err != nil {
		return 0, false, err
	}
	last, err := subtract.Binary(c.Context, blocks, one, c.FunctionInput())
	if err != nil {
		return 0, false, err
	}
	end, err := last.Integer()
	return end, false, err
}

func (c *Context) AdvancePgFreespace(n plan.InstalledPgFreespaceExpr, ctx context.Context, cursor *expr.SourceCursor) (value sqltype.Value, present bool, err error) {
	if ctx.Err() != nil || !cursor.Initialized() || cursor.Exhausted || !n.Kind.Set() || cursor.State == nil {
		return cursor.Next(ctx)
	}
	state, ok := cursor.State.(*pgFreespaceSource)
	if !ok || state.procedure != n.Procedure || state.reference != n.Ref || state.occurrence != n.Occurrence || state.source != n.Source || cursor.Type != n.Usage.Type {
		cursor.Close()
		return value, false, fmt.Errorf("stuntgres: invalid pg_freespace source state")
	}
	defer func() {
		if err != nil {
			cursor.Close()
		}
	}()
	local := *c
	if state.relation.IsNull() && (!c.PreserveParameterDemand || RandomLiteralNull(n.Args[0])) {
		local.declarationDemand = customNullFreespaceScalarElided
	}
	token, err := n.Info.EnterRoutine(&local, []sqltype.Value{state.relation})
	if err != nil {
		return value, false, err
	}
	defer token.Leave()
	defer func() {
		if err != nil {
			err = nativeDeclarationStatementContext(c, n.Procedure, err)
		}
	}()
	body, err := BeginNativeBody(&local, n.Procedure, n.Info)
	if err != nil {
		return value, false, err
	}
	if !state.initialized {
		cursor.Stop, state.empty, err = body.freespaceSeriesStop(n, state.relation)
		if err != nil {
			return value, false, err
		}
		state.initialized = true
	}
	_, info, err := freespaceCall(&body, n.Procedure, n.Info, "generate_series", "20,20")
	if err != nil {
		return value, false, err
	}
	integer := cursor.Type.CompositeMember(0).Usage.Type
	start, err := sqltype.Integer(integer, 0)
	if err != nil {
		return value, false, err
	}
	stop := sqltype.Null(integer)
	if !state.empty {
		stop, err = sqltype.Integer(integer, cursor.Stop)
		if err != nil {
			return value, false, err
		}
	}
	seriesToken, err := info.EnterRoutine(&body, []sqltype.Value{start, stop})
	if err != nil {
		return value, false, err
	}
	// The genuine bigint series advances here, before the projection's C2 call.
	if state.empty || cursor.Current > cursor.Stop {
		seriesToken.Leave()
		cursor.Close()
		return sqltype.Null(cursor.Type), false, nil
	}
	block, err := sqltype.Integer(cursor.Type.CompositeMember(0).Usage.Type, cursor.Current)
	cursor.Current++
	seriesToken.Leave()
	if err != nil {
		return value, false, err
	}
	scalar, info, err := freespaceCall(&body, n.Procedure, n.Info, "pg_freespace", "")
	if err != nil {
		return value, false, err
	}
	scalarToken, err := info.EnterRoutine(&body, []sqltype.Value{state.relation, block})
	if err != nil {
		return value, false, err
	}
	call := n
	call.Procedure, call.Info, call.Kind = scalar, info, functions.FreespaceBlock
	call.Info.Usage = cursor.Type.CompositeMember(1).Usage
	auth, _, err := RoutineInvocationAuthority(&body)
	if err != nil {
		scalarToken.Leave()
		return value, false, err
	}
	call.Ref = auth.Catalog.Owner().Domain.Ref(auth.Catalog.Epoch(), scalar.Address())
	// Contrib validates the selected scalar's formal types, not caller children.
	state.formals[1].Value = block
	call.Args = state.operands[:]
	call.Slots = [2]uint8{0, 1}
	avail, err := body.pgFreespaceBlock(call, state.relation, block, cursor.Type.CompositeMember(1).Usage.Type)
	scalarToken.Leave()
	if err != nil {
		return value, false, err
	}
	value, err = sqltype.OwnedCompositeValue(cursor.Type, []sqltype.Value{block, avail})
	return value, err == nil, err
}
