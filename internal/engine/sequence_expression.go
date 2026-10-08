package engine

import (
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
	identity bool
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
	if b.defaultConsumer {
		return nil, expressionError("0A000", "functions are unsupported", r.SourcePosition())
	}
	name := r.Name.Parts[len(r.Name.Parts)-1].Text
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
			node, ref, err = b.bindRegclass(raw)
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
		if b.expected != nil && b.expected.sequences != nil {
			for _, constant := range b.expected.sequences.constants {
				if constant.offset == literal.Value.Pos.Offset {
					ref, temporary, found = constant.ref, constant.temporary, true
					break
				}
			}
		}
		if !found {
			auth, err := b.engine.authorizationLocked(b.op)
			if err != nil {
				return nil, ref, err
			}
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
		auth, err := b.engine.authorizationLocked(b.op)
		if err != nil {
			return nil, ref, err
		}
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
func (e *Engine) regclassLocked(auth AuthorizationView, text string) (ObjectRef, error) {
	input := text
	if input == "-" {
		input = "0"
	}
	if input != "" {
		numeric := true
		for _, c := range input {
			if c < '0' || c > '9' {
				numeric = false
				break
			}
		}
		if numeric {
			n, err := strconv.ParseUint(input, 10, 32)
			if err != nil {
				return ObjectRef{}, sqlError("22003", fmt.Sprintf(`value "%s" is out of range for type oid`, text))
			}
			return ObjectRef{owner: e.owner, Epoch: auth.catalog.Epoch(), Address: ObjectAddress{ClassID: RelationClassID, ObjectID: OID(n)}}, nil
		}
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
