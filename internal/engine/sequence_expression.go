package engine

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type sequenceConstant struct {
	offset    int
	ref       ObjectRef
	temporary SessionToken
	stamp     DefinitionRevision
}
type sequenceCallExpression struct {
	expressionInfo
	name      string
	args      []BoundExpr
	ref       ObjectRef
	temporary SessionToken
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
	diagnostic := expressionError("42883", fmt.Sprintf("function %s(%s) does not exist", objectNameText(r.Name), strings.Join(names, ", ")), r.SourcePosition()).(*sqlstate.Error)
	diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	return diagnostic
}
func (b *expressionBinder) bindSequenceCall(r parser.CallExpr) (BoundExpr, error) {
	name := r.Name.Parts[len(r.Name.Parts)-1].Text
	if b.defaultConsumer && (name != "nextval" || len(r.Args) != 1) {
		return nil, expressionError("0A000", "functions are unsupported", r.SourcePosition())
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
	args := make([]BoundExpr, count)
	properties := ExprProperties{Volatility: Stable}
	if name == "nextval" || name == "setval" {
		properties.Volatility = Volatile
	}
	var ref ObjectRef
	for i, raw := range r.Args {
		var node BoundExpr
		var err error
		if i == 0 {
			if b.defaultConsumer {
				// REGCLASS is admitted only for this closed nextval argument.
				b.defaultConsumer = false
				node, ref, err = b.bindRegclass(raw)
				b.defaultConsumer = true
				literal, ok := node.(literalExpression)
				if err == nil && (!ok || literal.value.IsNull() || ref == (ObjectRef{})) {
					return nil, expressionError("0A000", "unsupported sequence DEFAULT argument", parser.ExpressionStart(raw))
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
	var temporary SessionToken
	for _, constant := range b.sequenceConstants {
		if constant.ref == ref {
			temporary = constant.temporary
		}
	}
	if b.defaultConsumer {
		if temporary != (SessionToken{}) {
			return nil, expressionError("0A000", "temporary sequence DEFAULT is unsupported", r.SourcePosition())
		}
		// Stored defaults do not enter call-site prepared literal reanalysis.
		b.sequenceConstants = nil
	}
	for _, arg := range args {
		if literal, ok := arg.(literalExpression); ok && literal.value.IsNull() {
			// PostgreSQL simplifies these four strict builtins with a known NULL
			// input even when another input is volatile. Keep analysis captures
			// and parameter types, but never evaluate the eliminated argument tree.
			typ := b.typ(sqltype.Int8)
			return literalExpression{b.info(typ.Usage(), r.SourcePosition(), ExprProperties{}), sqltype.Null(typ)}, nil
		}
	}
	return sequenceCallExpression{expressionInfo: b.info(b.typ(sqltype.Int8).Usage(), r.SourcePosition(), properties), name: name, args: args, ref: ref, temporary: temporary}, nil
}
func (b *expressionBinder) bindRegclass(raw parser.Expr) (BoundExpr, ObjectRef, error) {
	if b.defaultConsumer {
		return nil, ObjectRef{}, expressionError("0A000", "regclass is unsupported in DEFAULT expressions", parser.ExpressionStart(raw))
	}
	if err := qualifiedCastChild(raw); err != nil {
		return nil, ObjectRef{}, err
	}
	if cast, ok := raw.(parser.RegclassExpr); ok {
		return b.bindRegclass(cast.Child)
	}
	typ := b.typ(sqltype.Regclass)
	if literal, ok := raw.(parser.LiteralExpr); ok && literal.Value.Kind == parser.StringValue {
		ref := ObjectRef{}
		found := false
		var temporary SessionToken
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
			if d, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor); ok {
				if ns, ok := auth.catalog.state.descriptors[d.namespace].(*namespaceDescriptor); ok && ns.temporary == b.op.session.token {
					temporary = ns.temporary
				}
			}
		}
		var stamp DefinitionRevision
		// Capture the provider stamp from the same authority as its name and
		// temporary lifetime, never from a different published catalog.
		if d, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor); ok {
			stamp = d.sequencePlanStamp
		}
		b.sequenceConstants = append(b.sequenceConstants, sequenceConstant{offset: literal.Value.Pos.Offset, ref: ref, temporary: temporary, stamp: stamp})
		text := strconv.FormatUint(uint64(ref.Address.ObjectID), 10)
		return literalExpression{b.info(typ.Usage(), literal.Value.Pos, ExprProperties{}), sqltype.String(typ, text)}, ref, nil
	}
	node, err := b.bind(raw, expressionPolicy{context: functionArgumentContext, expected: typ.Usage(), mode: sqltype.Implicit})
	return node, ObjectRef{}, err
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
func regclassNumeric(text string) (uint32, bool, error) {
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
	n, err := strconv.ParseUint(input, 10, 32)
	if err != nil {
		return 0, true, sqlError("22003", fmt.Sprintf(`value "%s" is out of range for type oid`, text))
	}
	return uint32(n), true, nil
}

func (e *Engine) regclassLocked(auth AuthorizationView, text string) (ObjectRef, error) {
	if oid, numeric, err := regclassNumeric(text); numeric {
		if err != nil {
			return ObjectRef{}, err
		}
		return ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: ObjectAddress{ClassID: RelationClassID, ObjectID: OID(oid)}}, nil
	}
	name, err := parser.ParseRegclassName(text)
	if err != nil {
		return ObjectRef{}, sqlError("42602", "invalid name syntax")
	}
	if len(name.Parts) == 3 {
		return ObjectRef{}, sqlError("0A000", fmt.Sprintf(`cross-database references are not implemented: "%s"`, objectNameText(name)))
	}
	return e.resolveRelationReference(auth, name, false)
}

// Element input retains the statement's immutable authority, including its
// already-selected relation path. It never locks or consults a later catalog.
type capturedRegclassInput struct {
	auth      AuthorizationView
	path      []ObjectAddress
	temporary ObjectAddress
	token     SessionToken
}

func (e *Engine) captureRegclassInput(auth AuthorizationView) (sqltype.RegclassResolver, error) {
	path, err := e.referencePath(auth)
	if err != nil {
		return nil, err
	}
	input := capturedRegclassInput{auth: auth, path: path, temporary: temporaryNamespace(auth)}
	if auth.session != nil {
		input.token = auth.session.token
	}
	input.auth.session = nil
	input.auth.settings = nil
	return input, nil
}
func (r capturedRegclassInput) ResolveRegclass(ctx context.Context, text string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if oid, numeric, err := regclassNumeric(text); numeric {
		return oid, err
	}
	name, err := parser.ParseRegclassName(text)
	if err != nil {
		return 0, sqlError("42602", "invalid name syntax")
	}
	if len(name.Parts) == 3 {
		return 0, sqlError("0A000", fmt.Sprintf(`cross-database references are not implemented: "%s"`, objectNameText(name)))
	}
	path := r.path
	if len(name.Parts) == 2 {
		ns := r.auth.catalog.state.names[NameKey{ClassID: NamespaceClassID, Name: name.Parts[0].Text}]
		if name.Parts[0].Text == "pg_temp" {
			ns = r.temporary
		}
		if ns == (ObjectAddress{}) {
			path = nil
		} else {
			namespace := r.auth.catalog.state.descriptors[ns].(*namespaceDescriptor)
			if namespace.temporary != (SessionToken{}) && namespace.temporary != r.token {
				return 0, sqlError("42501", fmt.Sprintf("permission denied for schema %s", namespace.name))
			}
			if err := r.auth.CanUse(ObjectRef{owner: r.auth.engine.owner, Epoch: r.auth.catalog.Epoch(), Address: ns}); err != nil {
				return 0, err
			}
			path = []ObjectAddress{ns}
		}
	}
	for _, ns := range path {
		if address, ok := r.auth.catalog.state.names[NameKey{ClassID: RelationClassID, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text}]; ok {
			if _, ok := r.auth.catalog.state.descriptors[address].(*relationDescriptor); !ok {
				return 0, fmt.Errorf("stuntgres: invalid relation descriptor")
			}
			return uint32(address.ObjectID), nil
		}
	}
	return 0, sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, objectNameText(name)))
}
func (x sequenceCallExpression) Eval(c *EvalContext) (sqltype.Value, error) {
	if err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	if c.statementScope == nil {
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
			return sqltype.Null(x.usage.Type), nil
		}
	}
	scope := c.statementScope
	e := scope.op.session.Engine()
	ref := x.ref
	if x.name != "lastval" && ref.owner == nil {
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
		ref = ObjectRef{owner: e.owner, Epoch: scope.txRef.epoch, Address: ObjectAddress{ClassID: RelationClassID, ObjectID: OID(oid)}}
	}
	if x.name == "lastval" {
		e.mu.Lock()
		ref = ObjectRef{owner: e.owner, Epoch: e.state.epoch, Address: scope.op.session.lastSequence}
		e.mu.Unlock()
		if ref.Address == (ObjectAddress{}) {
			return sqltype.Value{}, sqlError("55000", "lastval is not yet defined in this session")
		}
	}
	e.mu.Lock()
	before, beforeErr := e.authorizationLocked(scope.op)
	if beforeErr == nil && scope.catalog != nil {
		before.catalog = e.privateCatalogLocked(scope.catalog)
	}
	if beforeErr == nil && ref.owner == e.owner && ref.Epoch != before.catalog.Epoch() && x.temporary == scope.op.session.token {
		if d, ok := before.catalog.state.descriptors[ref.Address].(*relationDescriptor); ok && d.relationKind == sequenceRelation {
			if ns, ok := before.catalog.state.descriptors[d.namespace].(*namespaceDescriptor); ok && ns.temporary == x.temporary {
				ref.Epoch = before.catalog.Epoch()
			}
		}
	}
	if beforeErr == nil && (ref.owner != e.owner || ref.Epoch != before.catalog.Epoch()) {
		beforeErr = ErrCatalogReplaced
	}
	if beforeErr == nil {
		d, ok := before.catalog.state.descriptors[ref.Address].(*relationDescriptor)
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
	if err := scope.acquireSequence(ref, rowExclusive, true); err != nil {
		return sqltype.Value{}, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	auth, err := e.authorizationLocked(scope.op)
	if err != nil {
		return sqltype.Value{}, err
	}
	if scope.catalog != nil {
		auth.catalog = e.privateCatalogLocked(scope.catalog)
	}
	d, ok := auth.catalog.state.descriptors[ref.Address].(*relationDescriptor)
	if !ok {
		if x.name == "lastval" {
			return sqltype.Value{}, sqlError("55000", "lastval is not yet defined in this session")
		}
		return sqltype.Value{}, sqlError("XX000", fmt.Sprintf("could not open relation with OID %d", ref.Address.ObjectID))
	}
	if err = sequenceKind(d); err != nil {
		return sqltype.Value{}, err
	}
	mask := UsagePrivilege | SelectPrivilege
	if x.name == "nextval" {
		mask = UsagePrivilege | UpdatePrivilege
	}
	if x.name == "setval" {
		mask = UpdatePrivilege
	}
	if !x.identity {
		if err = auth.checkSequenceAny(ref, mask); err != nil {
			return sqltype.Value{}, err
		}
	}
	if scope.owner.readOnly && (x.name == "nextval" || x.name == "setval") {
		ns, _ := auth.catalog.state.descriptors[d.namespace].(*namespaceDescriptor)
		if ns == nil || ns.temporary != scope.op.session.token {
			return sqltype.Value{}, sqlError("25006", "cannot execute "+x.name+"() in a read-only transaction")
		}
	}
	session := scope.op.session
	g := e.sequenceGeneratorLocked(scope, ref.Address)
	if g == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: sequence state missing")
	}
	if session.sequences == nil {
		session.sequences = make(map[ObjectAddress]*sequenceMemory)
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
			first, count, err := reserveSequence(*d.sequence, g, d.name)
			if err != nil {
				return sqltype.Value{}, err
			}
			m.next, m.remaining = first, count
		}
		result = m.next
		m.remaining--
		if m.remaining != 0 {
			m.next += d.sequence.increment
		}
		m.value, m.valid = result, true
		session.lastSequence = d.address
	case "setval":
		result, err = values[1].Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		if result < d.sequence.min || result > d.sequence.max {
			return sqltype.Value{}, sqlError("22003", fmt.Sprintf(`setval: value %d is out of bounds for sequence "%s" (%d..%d)`, result, d.name, d.sequence.min, d.sequence.max))
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
		switch x.usage.Type.ID() {
		case sqltype.Int2:
			result = int64(int16(result))
		case sqltype.Int4:
			result = int64(int32(result))
		}
	}
	return sqltype.Integer(x.usage.Type, result)
}
