package engine

import (
	"context"
	"fmt"
	"strconv"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/sequence"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

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
		if b.expected.Sequences != nil {
			for _, constant := range b.expected.Sequences.Constants {
				if constant.Offset == literal.Value.Pos.Offset {
					ref, temporary, found = constant.Ref, constant.Temporary, true
					break
				}
			}
		}
		if !found {
			// Resolve only uncaptured literals; held constants retain their exact
			// provider identity rather than following a later name replacement.
			ref, err = b.host.Regclass(auth, literal.Value.Text)
			if err != nil {
				return nil, ref, sequenceInputError(err, literal.Value.Pos, "", 0)
			}
			if d, ok := b.authorityRelation(auth, ref.Address); ok {
				if ns, ok := b.authorityNamespace(auth, d.Namespace()); ok && ns.Temporary == bindingSessionToken(b.host) {
					temporary = ns.Temporary
				}
			}
		}
		var stamp ids.DefinitionRevision
		// Capture the provider stamp from the same authority as its name and
		// temporary lifetime, never from a different published catalog.
		d, represented := b.authorityRelation(auth, ref.Address)
		if represented {
			stamp = b.host.DefinitionStamp(auth, d)
		}
		b.prepared.SequenceConstants = append(b.prepared.SequenceConstants, sequence.Constant{Offset: literal.Value.Pos.Offset, Ref: ref, Temporary: temporary, Stamp: stamp, Represented: represented})
		text := strconv.FormatUint(uint64(ref.Address.ObjectID), 10)
		info := b.info(typ.Usage(), literal.Value.Pos, expr.Properties{})
		if _, ok := b.authorityRelation(auth, ref.Address); ok {
			info.RegclassRef = ref
		}
		return plan.LiteralExpr{Info: info, Value: sqltype.String(typ, text)}, ref, nil
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
	coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), typ.Usage(), sqltype.Implicit)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	info, err := b.collatedInfo(b.info(typ.Usage(), raw.SourcePosition(), analyze.CoercionProperties(node, coercionPlan)), node)
	if err != nil {
		return nil, ids.ObjectRef{}, err
	}
	return plan.CoercionExpr{Info: info, Child: node, Plan: coercionPlan, Mode: sqltype.Implicit}, ids.ObjectRef{}, nil
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
	coercionPlan, err := sqltype.PlanCoercion(child.ResultUsage(), target, sqltype.Explicit)
	if err != nil {
		return nil, expr.ErrorAt("42846", fmt.Sprintf("cannot cast type %s to regclass", b.diagnosticTypeName(child.ResultUsage().Type)), pos)
	}
	if child.ResultUsage() == target {
		return child, nil
	}
	return plan.CoercionExpr{Info: b.info(target, raw.SourcePosition(), analyze.CoercionProperties(child, coercionPlan)), Child: child, Plan: coercionPlan, Mode: sqltype.Explicit}, nil
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
		return e.catalog.Domain.Ref(auth.Catalog.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}), nil
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
			return parser.ObjectName{}, sqlError("0A000", fmt.Sprintf(`cross-database references are not implemented: "%s"`, catalog.ObjectNameText(name)))
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
				return 0, sqlError("42501", fmt.Sprintf("permission denied for schema %s", namespace.Name()))
			}
			if err := r.auth.CanUse(r.auth.Ref(ns)); err != nil {
				return 0, err
			}
			path = []ids.ObjectAddress{ns}
		}
	}
	for _, ns := range path {
		if address := r.auth.definitionLookup(ids.NameKey{ClassID: ids.RelationClassID, Namespace: ns, Name: name.Parts[len(name.Parts)-1].Text}); address != (ids.ObjectAddress{}) {
			descriptor, _ := r.auth.definitionDescriptor(address)
			if _, ok := descriptor.(*catalog.Relation); !ok {
				return 0, fmt.Errorf("stuntgres: invalid relation descriptor")
			}
			return uint32(address.ObjectID), nil
		}
	}
	return 0, sqlError("42P01", fmt.Sprintf(`relation "%s" does not exist`, catalog.ObjectNameText(name)))
}
