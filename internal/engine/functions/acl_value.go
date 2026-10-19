package functions

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// ACLKind selects an ACL value call.
type ACLKind uint8

const (
	ACLDefault ACLKind = iota
	ACLMake
	ACLExplode
	// ACLContains is the aclitem[] @> aclitem operator.
	ACLContains
)

// Name is the call's function name, or the operator spelling.
func (k ACLKind) Name() string {
	switch k {
	case ACLDefault:
		return "acldefault"
	case ACLMake:
		return "makeaclitem"
	case ACLExplode:
		return "aclexplode"
	default:
		return "@>"
	}
}

var aclValueTypes = [4][4]sqltype.ID{
	{sqltype.Char, sqltype.Oid},
	{sqltype.Oid, sqltype.Oid, sqltype.Text, sqltype.Bool},
	{sqltype.ACLItemArray},
	{sqltype.ACLItemArray, sqltype.ACLItem},
}
var aclValueArity = [4]int{2, 4, 1, 2}

func (k ACLKind) types() []sqltype.ID {
	return aclValueTypes[k][:aclValueArity[k]]
}

// ACLValue is acldefault, makeaclitem, aclexplode or aclitem[] @> aclitem.
// Args are stored in written order, never signature/evaluation order aliases;
// slots beyond the kind's arity are nil. Named marks aclexplode(acl => ...);
// Namespace is the explicit operator schema of @>, if written.
type ACLValue struct {
	expr.Info
	Kind      ACLKind
	Args      [4]expr.Expr
	Named     bool
	Namespace string
}

func (n ACLValue) baseProperties() expr.Properties {
	p := expr.Properties{}
	if n.Kind == ACLExplode {
		p.Volatility = expr.Stable
	}
	return p
}

// aclRuntime recovers the ACL capability from the evaluation context.
func aclRuntime(c expr.Context) (ACLRuntime, error) {
	runtime, ok := c.(ACLRuntime)
	if !ok {
		return nil, fmt.Errorf("stuntgres: ACL evaluation lacks the ACL runtime")
	}
	return runtime, nil
}

func (n ACLValue) values(c expr.Context) ([4]sqltype.Value, bool, error) {
	var values [4]sqltype.Value
	if _, err := evaluationContext(c); err != nil {
		return values, false, err
	}
	null := false
	for i, arg := range n.Children() {
		v, err := arg.Eval(c)
		if err != nil {
			return values, false, err
		}
		values[i] = v
		null = null || v.IsNull()
	}
	return values, null, nil
}

func (n ACLValue) Eval(c expr.Context) (sqltype.Value, error) {
	values, null, err := n.values(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	runtime, err := aclRuntime(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if n.Kind == ACLContains {
		ok, err := aclContains(c.FunctionContext(), runtime, values[0], values[1])
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Boolean(n.Usage.Type, ok), nil
	}
	if n.Kind == ACLExplode {
		return sqltype.Value{}, fmt.Errorf("stuntgres: ACL set call requires an invocation")
	}
	authority := runtime.ACLAuthority()
	if authority == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: ACL constructor lacks statement authority")
	}
	if n.Kind == ACLMake {
		return n.makeItem(authority, values)
	}
	return n.defaultACL(authority, values)
}

func (n ACLValue) makeItem(authority *sqltype.ACLAuthority, values [4]sqltype.Value) (sqltype.Value, error) {
	grantee, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	grantor, err := values[1].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	text, err := values[2].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	mask, err := aclPrivilegeNames(text)
	if err != nil {
		return sqltype.Value{}, err
	}
	grantable, err := values[3].Boolean()
	if err != nil {
		return sqltype.Value{}, err
	}
	options := uint32(0)
	if grantable {
		options = mask
	}
	return sqltype.ACLItemValue(n.Usage.Type, authority, grantee, grantor, mask, options)
}

func (n ACLValue) defaultACL(authority *sqltype.ACLAuthority, values [4]sqltype.Value) (sqltype.Value, error) {
	code, err := values[0].Text()
	if err != nil {
		return sqltype.Value{}, err
	}
	owner, err := values[1].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	own, public, err := aclDefaultMasks(code)
	if err != nil {
		return sqltype.Value{}, err
	}
	if own == 0 && public == 0 {
		return sqltype.EmptyACLArrayValue(n.Usage.Type)
	}
	count := 1
	if public != 0 {
		count++
	}
	items := make([]sqltype.Value, 0, count)
	if public != 0 {
		v, err := sqltype.ACLItemValue(n.Usage.Type.Element(), authority, 0, owner, public, 0)
		if err != nil {
			return sqltype.Value{}, err
		}
		items = append(items, v)
	}
	if own != 0 {
		v, err := sqltype.ACLItemValue(n.Usage.Type.Element(), authority, owner, owner, own, 0)
		if err != nil {
			return sqltype.Value{}, err
		}
		items = append(items, v)
	}
	return sqltype.OwnedArrayValue(n.Usage.Type, []sqltype.ArrayDimension{{Lower: 1, Length: int32(len(items))}}, items)
}

// OpenExplode starts one aclexplode invocation. A NULL argument yields a
// closed cursor; the array is validated before the first row.
func (n ACLValue) OpenExplode(c expr.Context) (ACLExplodeCursor, error) {
	values, null, err := n.values(c)
	if err != nil {
		return ACLExplodeCursor{}, err
	}
	if null {
		return ACLExplodeCursor{closed: true}, nil
	}
	runtime, err := aclRuntime(c)
	if err != nil {
		return ACLExplodeCursor{}, err
	}
	if err := checkACLValue(c.FunctionContext(), runtime, values[0]); err != nil {
		return ACLExplodeCursor{}, err
	}
	return ACLExplodeCursor{array: values[0]}, nil
}

// Validate checks the stored recipe: signature, properties, result and
// notation.
func (n ACLValue) Validate() error {
	if n.Kind > ACLContains || n.Usage.Type == nil {
		return fmt.Errorf("stuntgres: invalid stored ACL recipe")
	}
	p := n.baseProperties()
	for i, t := range n.Kind.types() {
		arg := n.Args[i]
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != t {
			return fmt.Errorf("stuntgres: invalid stored ACL signature")
		}
		p = expr.MergeProperties(p, arg.Properties())
	}
	if n.Property != p {
		return fmt.Errorf("stuntgres: invalid stored ACL properties")
	}
	if n.Usage != n.Usage.Type.Usage() || n.Named && n.Kind != ACLExplode || n.Namespace != "" && (n.Kind != ACLContains || n.Namespace != "pg_catalog") {
		return fmt.Errorf("stuntgres: invalid stored ACL result or notation")
	}
	for _, arg := range n.Args[aclValueArity[n.Kind]:] {
		if arg != nil {
			return fmt.Errorf("stuntgres: surplus stored ACL argument")
		}
	}
	result := sqltype.ACLItemArray
	if n.Kind == ACLMake {
		result = sqltype.ACLItem
	}
	if n.Kind == ACLContains {
		result = sqltype.Bool
	}
	if n.Kind != ACLExplode {
		if n.Usage.Type.ID() != result {
			return fmt.Errorf("stuntgres: invalid stored ACL result")
		}
		return nil
	}
	shape := n.Usage.Type
	labels := [4]string{"grantor", "grantee", "privilege_type", "is_grantable"}
	types := [4]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Text, sqltype.Bool}
	if shape.ID() != 2249 || !shape.IsComposite() || shape.CompositeLen() != 4 {
		return fmt.Errorf("stuntgres: invalid stored ACL record")
	}
	for i, label := range labels {
		m := shape.CompositeMember(i)
		if m.Name != label || m.Attribute != int32(i+1) || m.Usage != m.Usage.Type.Usage() || m.Usage.Type.ID() != types[i] {
			return fmt.Errorf("stuntgres: invalid stored ACL record member")
		}
	}
	return nil
}

// Children are the written arguments of the kind's arity.
func (n ACLValue) Children() []expr.Expr { return n.Args[:aclValueArity[n.Kind]] }

func (n ACLValue) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if int(n.Kind) >= len(aclValueArity) {
		return nil, fmt.Errorf("stuntgres: invalid stored ACL recipe")
	}
	if err := checkChildren("ACL value", children, aclValueArity[n.Kind]); err != nil {
		return nil, err
	}
	n.Info = info
	copy(n.Args[:], children)
	return n, nil
}

// IntrinsicVolatility: aclexplode reads the statement's role snapshot; the
// constructors and containment are immutable.
func (n ACLValue) IntrinsicVolatility() expr.Volatility { return stableIf(n.Kind == ACLExplode) }

// PlanningFold keeps the calls symbolic: their inert identities need the
// statement authority, and stored definitions retain their child dependencies.
func (n ACLValue) PlanningFold() expr.Fold { return expr.FoldNever }

func (n ACLValue) ValidateStoredProvider() error { return n.Validate() }

// LooksLikeFunction: the calls display as function calls in index keys; the
// @> operator does not.
func (n ACLValue) LooksLikeFunction() bool { return n.Kind != ACLContains }

// PreflightFold: preparation only preflights the children; the calls stay
// symbolic, as in planning.
func (n ACLValue) PreflightFold() expr.Preflight { return expr.PreflightChildren }

// SameGroup implements expr.GroupIdentity: the kind, named notation and
// operator schema are part of the identity in every comparison.
func (n ACLValue) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(ACLValue)
	return ok && n.Kind == o.Kind && n.Named == o.Named && n.Namespace == o.Namespace && sameChildren(n.Children(), o.Children(), same)
}

// sameChildren compares two child lists of equal shape with same.
func sameChildren(a, b []expr.Expr, same func(a, b expr.Expr) bool) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !same(a[i], b[i]) {
			return false
		}
	}
	return true
}

// Render displays the calls schema-qualified with their implicit argument
// casts, and the operator always parenthesized, written with its schema.
func (n ACLValue) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	children := n.Children()
	args := make([]expr.RenderArg, len(children))
	for i, arg := range children {
		args[i] = expr.RenderArg{Expr: arg}
	}
	spec := expr.Rendering{
		Tree:   fmt.Sprintf("ACLValue %d named %t namespace %s", n.Kind, n.Named, n.Namespace),
		Inputs: children,
		Args:   args,
	}
	if n.Kind == ACLContains {
		op := "@>"
		if n.Namespace != "" {
			op = `OPERATOR("` + strings.ReplaceAll(n.Namespace, `"`, `""`) + `".@>)`
		}
		spec.Form = expr.FormTemplate
		spec.Pieces = []expr.Piece{expr.TextPiece("("), expr.ArgPiece(0), expr.TextPiece(" " + op + " "), expr.ArgPiece(1), expr.TextPiece(")")}
		return spec, nil
	}
	if n.Named {
		spec.Args[0].Label = "acl"
	}
	spec.Form = expr.FormCall
	spec.Name = "pg_catalog." + n.Kind.Name()
	spec.Quote = expr.QuoteVerbatim
	spec.LabelQuote = expr.QuoteVerbatim
	spec.Replay.Name = n.Kind.Name()
	return spec, nil
}

func (e ACLValue) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction && kind != expr.CaptureOperator {
		return expr.CaptureOperands{}, false, nil
	}
	if int(e.Kind) >= len(aclValueArity) {
		return captureShape(-1, len(e.Args))
	}
	return captureShape(aclValueArity[e.Kind], len(e.Args))
}

func (e ACLValue) CaptureOperand(written int) (expr.Expr, error) {
	if int(e.Kind) >= len(aclValueArity) {
		return nil, fmt.Errorf("invalid capture count")
	}
	return captureAt(e.Args[:], aclValueArity[e.Kind], written)
}
