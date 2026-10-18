package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type sequenceConstant struct {
	offset      int
	ref         ids.ObjectRef
	temporary   ids.SessionToken
	stamp       ids.DefinitionRevision
	represented bool
}
type sequenceCallExpression struct {
	expr.Info
	name      string
	args      []expr.Expr
	ref       ids.ObjectRef
	temporary ids.SessionToken
	identity  bool
}

func (b *expressionBinder) sequenceSignatureError(r parser.CallExpr) error {
	names := make([]string, len(r.Args))
	for i, raw := range r.Args {
		names[i] = "unknown"
		switch v := raw.(type) {
		case parser.RegclassExpr:
			names[i] = "regclass"
		case parser.TypedLiteralExpr:
			names[i] = v.TypeName.Name
		case parser.LiteralExpr:
			switch v.Value.Kind {
			case parser.BitStringValue:
				names[i] = "bit"
			case parser.BooleanValue:
				names[i] = "boolean"
			case parser.NumericValue:
				names[i] = "numeric"
			case parser.IntegerValue:
				names[i] = "integer"
				if v.Value.Integer < -2147483648 || v.Value.Integer > 2147483647 {
					names[i] = "bigint"
				}
			}
		default:
			if node, err := b.bind(raw, expressionPolicy{}); err == nil && node.ResultUsage().Type != nil {
				names[i] = node.ResultUsage().Type.DiagnosticName()
			}
		}
	}
	diagnostic := expr.ErrorAt("42883", fmt.Sprintf("function %s(%s) does not exist", objectNameText(r.Name), strings.Join(names, ", ")), r.SourcePosition()).(*sqlstate.Error)
	diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	return diagnostic
}
func (b *expressionBinder) bindSequenceCall(r parser.CallExpr) (expr.Expr, error) {
	name := r.Name.Parts[len(r.Name.Parts)-1].Text
	if b.defaultConsumer && (name != "nextval" || len(r.Args) != 1) {
		return nil, expr.ErrorAt("0A000", "functions are unsupported", r.SourcePosition())
	}
	visible, err := b.builtinVisible(r.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, b.sequenceSignatureError(r)
	}
	count := len(r.Args)
	want := 1
	if name == "lastval" {
		want = 0
	}
	if name == "setval" {
		want = 2
	}
	if count != want && !(name == "setval" && count == 3) {
		return nil, b.sequenceSignatureError(r)
	}
	args := make([]expr.Expr, count)
	properties := expr.Properties{Volatility: expr.Stable}
	if name == "nextval" || name == "setval" {
		properties.Volatility = expr.Volatile
	}
	var ref ids.ObjectRef
	for i, raw := range r.Args {
		var node expr.Expr
		var err error
		if i == 0 {
			if b.defaultConsumer {
				node, ref, err = b.bindRegclass(raw)
				literal, ok := node.(literalExpression)
				if err == nil && (!ok || literal.value.IsNull() || ref == (ids.ObjectRef{})) {
					return nil, expr.ErrorAt("0A000", "unsupported sequence DEFAULT argument", parser.ExpressionStart(raw))
				}
			} else {
				node, ref, err = b.bindRegclass(raw)
			}
		} else {
			id := sqltype.Int8
			if i == 2 {
				id = sqltype.Bool
			}
			node, err = b.bind(raw, expressionPolicy{context: functionArgumentContext, expected: b.typ(id).Usage(), mode: sqltype.Implicit})
		}
		if err != nil {
			if d, ok := err.(*sqlstate.Error); ok && (d.Code == "42804" || d.Code == "42846") {
				return nil, b.sequenceSignatureError(r)
			}
			return nil, err
		}
		args[i] = node
		properties.ParameterDependent = properties.ParameterDependent || node.Properties().ParameterDependent
		properties.RowDependent = properties.RowDependent || node.Properties().RowDependent
		if node.Properties().Volatility > properties.Volatility {
			properties.Volatility = node.Properties().Volatility
		}
	}
	var temporary ids.SessionToken
	for _, constant := range b.sequenceConstants {
		if constant.ref == ref {
			temporary = constant.temporary
		}
	}
	if b.defaultConsumer {
		if err := validateDefaultTemporaryProvider(temporary, b.defaultTemporary, r.SourcePosition()); err != nil {
			return nil, err
		}
		// Stored defaults do not enter call-site prepared literal reanalysis.
		b.sequenceConstants = nil
	}
	formal := [3]sqltype.ID{sqltype.Regclass, sqltype.Int8, sqltype.Bool}
	info, err := b.builtinRoutineInfo(b.info(b.typ(sqltype.Int8).Usage(), r.SourcePosition(), properties), sequenceRoutineProvider, name, formal[:count]...)
	if err != nil {
		return nil, err
	}
	for _, arg := range args {
		if literal, ok := arg.(literalExpression); ok && literal.value.IsNull() {
			// PostgreSQL simplifies these four strict builtins with a known NULL
			// input even when another input is volatile. Keep analysis captures
			// and parameter types, but never evaluate the eliminated argument tree.
			typ := b.typ(sqltype.Int8)
			return literalExpression{b.info(typ.Usage(), r.SourcePosition(), expr.Properties{}), sqltype.Null(typ)}, nil
		}
	}
	return sequenceCallExpression{Info: info, name: name, args: args, ref: ref, temporary: temporary}, nil
}
func (b *expressionBinder) bindRegclass(raw parser.Expr) (expr.Expr, ids.ObjectRef, error) {
	if err := qualifiedCastChild(raw); err != nil {
		return nil, ids.ObjectRef{}, err
	}
	if cast, ok := raw.(parser.RegclassExpr); ok {
		return b.bindRegclass(cast.Child)
	}
	typ := b.typ(sqltype.Regclass)
	if literal, ok := raw.(parser.LiteralExpr); ok && literal.Value.Kind == parser.StringValue {
		ref := ids.ObjectRef{}
		found := false
		var temporary ids.SessionToken
		auth, err := b.bindingAuthorization()
		if err != nil {
			return nil, ref, err
		}
		if b.expected != nil && b.expected.sequences != nil {
			for _, constant := range b.expected.sequences.constants {
				if constant.offset == literal.Value.Pos.Offset {
					ref, temporary, found = constant.ref, constant.temporary, true
					break
				}
			}
		}
		if !found {
			// Resolve only uncaptured literals; held constants retain their exact
			// provider identity rather than following a later name replacement.
			ref, err = b.engine.regclassLocked(auth, literal.Value.Text)
			if err != nil {
				return nil, ref, sequenceInputError(err, literal.Value.Pos, "", 0)
			}
			if d, ok := auth.definitionRelation(ref.Address); ok {
				if ns, ok := auth.definitionNamespace(d.namespace); ok && ns.Temporary == b.op.session.token {
					temporary = ns.Temporary
				}
			}
		}
		var stamp ids.DefinitionRevision
		// Capture the provider stamp from the same authority as its name and
		// temporary lifetime, never from a different published catalog.
		d, represented := auth.definitionRelation(ref.Address)
		if represented {
			stamp = preparedDefinitionStamp(d, auth.session)
		}
		b.sequenceConstants = append(b.sequenceConstants, sequenceConstant{offset: literal.Value.Pos.Offset, ref: ref, temporary: temporary, stamp: stamp, represented: represented})
		text := strconv.FormatUint(uint64(ref.Address.ObjectID), 10)
		info := b.info(typ.Usage(), literal.Value.Pos, expr.Properties{})
		if _, ok := auth.definitionRelation(ref.Address); ok {
			info.RegclassRef = ref
		}
		return literalExpression{info, sqltype.String(typ, text)}, ref, nil
	}
	// This legacy provider also owns explicit REGCLASS annotations and sequence
	// inputs. New contextual inquiry coercions must not widen its public boundary.
	node, err := b.bind(raw, expressionPolicy{context: functionArgumentContext, expected: typ.Usage(), mode: sqltype.Implicit, deferAssignment: true})
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	if node.ResultUsage().BaseUsage().Type != typ {
		return nil, ids.ObjectRef{}, sqlError("42804", "incompatible source and destination types")
	}
	if node.ResultUsage() == typ.Usage() {
		return node, ids.ObjectRef{}, nil
	}
	plan, err := sqltype.PlanCoercion(node.ResultUsage(), typ.Usage(), sqltype.Implicit)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	info, err := b.collatedInfo(b.info(typ.Usage(), raw.SourcePosition(), coercionProperties(node, plan)), node)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	return coercionExpression{info, node, plan, sqltype.Implicit, false}, ids.ObjectRef{}, nil
}

// Explicit casts share name input with sequence consumers but use the ordinary
// coercion matrix for typed sources. Keep coercion nodes: they are not stored
// regclass input constants and must not acquire relation dependencies.
func (b *expressionBinder) bindRegclassCast(raw parser.Expr, pos parser.Position) (expr.Expr, error) {
	if err := qualifiedCastChild(raw); err != nil {
		return nil, err
	}
	if literal, ok := raw.(parser.LiteralExpr); ok && literal.Value.Kind == parser.StringValue {
		node, _, err := b.bindRegclass(raw)
		return node, err
	}
	if err := b.captureElementInput(); err != nil {
		return nil, err
	}
	target := b.typ(sqltype.Regclass).Usage()
	child, err := b.bind(raw, expressionPolicy{context: projectionContext, expected: target, deferAssignment: true})
	if err != nil {
		return nil, err
	}
	plan, err := sqltype.PlanCoercion(child.ResultUsage(), target, sqltype.Explicit)
	if err != nil {
		return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to regclass", b.diagnosticTypeName(child.ResultUsage().Type)), pos)
	}
	if child.ResultUsage() == target {
		return child, nil
	}
	return coercionExpression{b.info(target, raw.SourcePosition(), coercionProperties(child, plan)), child, plan, sqltype.Explicit, false}, nil
}
func sequenceInputError(err error, pos parser.Position, context string, parameter int) error {
	if d, ok := err.(*sqlstate.Error); ok {
		copy := *d
		copy.Position = ""
		if parameter != 0 {
			copy.Context = fmt.Sprintf("unnamed portal parameter $%d = '...'", parameter)
		} else if pos.Character > 0 {
			copy.Position = strconv.Itoa(pos.Character)
		}
		return &copy
	}
	return err
}
func regclassNumeric(text string, typ *sqltype.Descriptor) (uint32, bool, error) {
	input := text
	if input == "-" {
		input = "0"
	}
	if input == "" {
		return 0, false, nil
	}
	for _, c := range input {
		if c < '0' || c > '9' {
			return 0, false, nil
		}
	}
	value, err := typ.Input(context.Background(), input, -1)
	if err != nil {
		return 0, true, err
	}
	n, err := value.OID()
	return n, true, err
}

func (e *Engine) regclassLocked(auth AuthorizationView, text string) (ids.ObjectRef, error) {
	typ, _ := auth.definitionType(sqltype.Oid)
	if oid, numeric, err := regclassNumeric(text, typ); numeric {
		if err != nil {
			return ids.ObjectRef{}, err
		}
		return e.catalog.domain.Ref(auth.catalog.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}), nil
	}
	name, err := parser.ParseRegclassName(text)
	if err != nil {
		return ids.ObjectRef{}, sqlError("42602", "invalid name syntax")
	}
	database := ""
	if auth.session != nil {
		database = auth.session.databaseName
	}
	name, err = regclassQualifiedName(name, text, database)
	if err != nil {
		return ids.ObjectRef{}, err
	}
	return e.resolveRelationReference(auth, name, false)
}

func regclassQualifiedName(name parser.ObjectName, text, database string) (parser.ObjectName, error) {
	if len(name.Parts) > 3 {
		return parser.ObjectName{}, sqlError("42601", "improper relation name (too many dotted names): "+text)
	}
	if len(name.Parts) == 3 {
		if name.Parts[0].Text != database {
			return parser.ObjectName{}, sqlError("0A000", fmt.Sprintf(`cross-database references are not implemented: "%s"`, objectNameText(name)))
		}
		name.Parts = name.Parts[1:]
	}
	return name, nil
}

// Element input retains the statement's immutable authority, including its
// already-selected relation path. It never locks or consults a later catalog.
type capturedRegclassInput struct {
	auth      AuthorizationView
	path      []ids.ObjectAddress
	temporary ids.ObjectAddress
	token     ids.SessionToken
	database  string
}

func (e *Engine) captureRegclassInput(auth AuthorizationView) (sqltype.RegclassResolver, error) {
	path, err := e.referencePath(auth)
	if err != nil {
		return nil, err
	}
	input := capturedRegclassInput{auth: auth, path: path, temporary: temporaryNamespace(auth)}
	if auth.session != nil {
		input.token = auth.session.token
		input.database = auth.session.databaseName
	}
	input.auth.session = nil
	input.auth.settings = nil
	return input, nil
}
func (r capturedRegclassInput) ResolveRegclass(ctx context.Context, text string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	typ, _ := r.auth.definitionType(sqltype.Oid)
	if oid, numeric, err := regclassNumeric(text, typ); numeric {
		return oid, err
	}
	name, err := parser.ParseRegclassName(text)
	if err != nil {
		return 0, sqlError("42602", "invalid name syntax")
	}
	name, err = regclassQualifiedName(name, text, r.database)
	if err != nil {
		return 0, err
	}
	path := r.path
	if len(name.Parts) == 2 {
		ns := r.auth.definitionLookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name.Parts[0].Text})
		if name.Parts[0].Text == "pg_temp" {
			ns = r.temporary
		}
		if ns == (ids.ObjectAddress{}) {
			path = nil
		} else {
			namespace, _ := r.auth.definitionNamespace(ns)
			if namespace.Temporary != (ids.SessionToken{}) && namespace.Temporary != r.token {
				return 0, sqlError("42501", fmt.Sprintf("permission denied for schema %s", namespace.name))
			}
			if err := r.auth.CanUse(r.auth.engine.catalog.domain.Ref(r.auth.catalog.Epoch(), ns)); err != nil {
				return 0, err
			}
			path = []ids.ObjectAddress{ns}
		}
	}
	for _, ns := range path {
		if address := r.auth.definitionLookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text}); address != (ids.ObjectAddress{}) {
			descriptor, _ := r.auth.definitionDescriptor(address)
			if _, ok := descriptor.(*relationDescriptor); !ok {
				return 0, fmt.Errorf("stuntgres: invalid relation descriptor")
			}
			return uint32(address.ObjectID), nil
		}
	}
	return 0, sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, objectNameText(name)))
}
func (x sequenceCallExpression) Eval(ctx expr.Context) (sqltype.Value, error) {
	c := evalContext(ctx)
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.liveScope() == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: sequence call lacks statement authority")
	}
	var values [3]sqltype.Value
	for i, arg := range x.args {
		v, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = v
	}
	for i := range x.args {
		if values[i].IsNull() {
			return sqltype.Null(x.Usage.Type), nil
		}
	}
	scope := c.liveScope()
	e := scope.op.session.Engine()
	ref := x.ref
	if x.name != "lastval" && ref.Domain() == nil {
		text, err := values[0].Text()
		if err != nil {
			return sqltype.Value{}, err
		}
		// Literal and imported parameter datums already contain canonical OIDs.
		// Never resolve a name here or retarget input after argument effects.
		oid, err := strconv.ParseUint(text, 10, 32)
		if err != nil {
			return sqltype.Value{}, err
		}
		ref = e.catalog.domain.Ref(scope.txRef.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)})
	}
	if x.name == "lastval" {
		e.mu.Lock()
		ref = e.catalog.domain.Ref(e.catalog.state.epoch, scope.op.session.lastSequence)
		e.mu.Unlock()
		if ref.Address == (ids.ObjectAddress{}) {
			return sqltype.Value{}, sqlError("55000", "lastval is not yet defined in this session")
		}
	}
	e.mu.Lock()
	before, beforeErr := scope.writeAuthorizationLocked(e)
	if beforeErr == nil && scope.catalog != nil {
		before.catalog = e.privateCatalogLocked(scope.catalog)
		before.recaptureCatalog(before.catalog)
	}
	if beforeErr == nil && ref.Epoch != before.catalog.Epoch() && x.temporary == scope.op.session.token && temporarySequenceEpochValid(before.catalog, ref, x.temporary) {
		ref.Epoch = before.catalog.Epoch()
	}
	if beforeErr == nil && (ref.Domain() != e.catalog.domain || ref.Epoch != before.catalog.Epoch()) {
		beforeErr = ids.ErrCatalogReplaced
	}
	if beforeErr == nil {
		d, ok := before.catalog.Relation(ref.Address)
		if !ok {
			if x.name == "lastval" {
				beforeErr = sqlError("55000", "lastval is not yet defined in this session")
			} else {
				beforeErr = sqlError("XX000", fmt.Sprintf("could not open relation with OID %d", ref.Address.ObjectID))
			}
		} else {
			beforeErr = sequenceKind(d)
		}
	}
	e.mu.Unlock()
	if beforeErr != nil {
		return sqltype.Value{}, beforeErr
	}
	if err := scope.acquireSequence(ref, lock.RowExclusive, true); err != nil {
		return sqltype.Value{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := scope.writeAuthorizationLocked(e)
	if err != nil {
		return sqltype.Value{}, err
	}
	if scope.catalog != nil {
		auth.catalog = e.privateCatalogLocked(scope.catalog)
		auth.recaptureCatalog(auth.catalog)
	}
	d, ok := auth.catalog.Relation(ref.Address)
	if !ok {
		if x.name == "lastval" {
			return sqltype.Value{}, sqlError("55000", "lastval is not yet defined in this session")
		}
		return sqltype.Value{}, sqlError("XX000", fmt.Sprintf("could not open relation with OID %d", ref.Address.ObjectID))
	}
	if err = sequenceKind(d); err != nil {
		return sqltype.Value{}, err
	}
	if ns, ok := auth.catalog.Namespace(d.namespace); ok && ns.Temporary == scope.op.session.token {
		scope.owner.accessedTemporary = true
	}
	mask := authorization.UsagePrivilege | authorization.SelectPrivilege
	if x.name == "nextval" {
		mask = authorization.UsagePrivilege | authorization.UpdatePrivilege
	}
	if x.name == "setval" {
		mask = authorization.UpdatePrivilege
	}
	if !x.identity {
		if err = auth.checkSequenceAny(ref, mask); err != nil {
			return sqltype.Value{}, err
		}
	}
	if scope.owner.readOnly && (x.name == "nextval" || x.name == "setval") {
		ns, _ := auth.catalog.Namespace(d.namespace)
		if ns == nil || ns.Temporary != scope.op.session.token {
			return sqltype.Value{}, sqlError("25006", "cannot execute "+x.name+"() in a read-only transaction")
		}
	}
	session := scope.op.session
	g := e.sequenceGeneratorLocked(scope, ref.Address)
	if g == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: sequence state missing")
	}
	if session.sequences == nil {
		session.sequences = make(map[ids.ObjectAddress]*sequenceMemory)
	}
	m := session.sequences[ref.Address]
	if m == nil {
		m = &sequenceMemory{}
		session.sequences[ref.Address] = m
	}
	if m.generation != g.generation {
		m.remaining = 0
		m.generation = g.generation
	}
	var result int64
	switch x.name {
	case "currval", "lastval":
		if !m.valid {
			msg := fmt.Sprintf(`currval of sequence "%s" is not yet defined in this session`, d.name)
			if x.name == "lastval" {
				msg = "lastval is not yet defined in this session"
			}
			return sqltype.Value{}, sqlError("55000", msg)
		}
		result = m.value
	case "nextval":
		if m.remaining == 0 {
			first, count, err := reserveSequence(*d.Sequence, g, d.name)
			if err != nil {
				return sqltype.Value{}, err
			}
			m.next, m.remaining = first, count
		}
		result = m.next
		m.remaining--
		if m.remaining != 0 {
			m.next += d.Sequence.increment
		}
		m.value, m.valid = result, true
		session.lastSequence = d.address
	case "setval":
		result, err = values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		if result < d.Sequence.min || result > d.Sequence.max {
			return sqltype.Value{}, sqlError("22003", fmt.Sprintf(`setval: value %d is out of bounds for sequence "%s" (%d..%d)`, result, d.name, d.Sequence.min, d.Sequence.max))
		}
		called := true
		if len(x.args) == 3 {
			called, err = values[2].Boolean()
			if err != nil {
				return sqltype.Value{}, err
			}
		}
		g.last, g.called = result, called
		m.remaining = 0
		if called {
			m.value, m.valid = result, true
		}
	}
	if x.identity {
		switch x.Usage.Type.ID() {
		case sqltype.Int2:
			result = int64(int16(result))
		case sqltype.Int4:
			result = int64(int32(result))
		}
	}
	return sqltype.Integer(x.Usage.Type, result)
}
