package engine

import (
	"fmt"
	"strings"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type aclValueKind uint8

const (
	aclDefault aclValueKind = iota
	aclMake
	aclExplode
	aclContains
)

func (k aclValueKind) name() string {
	switch k {
	case aclDefault:
		return "acldefault"
	case aclMake:
		return "makeaclitem"
	case aclExplode:
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

func (k aclValueKind) types() []sqltype.ID {
	return aclValueTypes[k][:aclValueArity[k]]
}

// Children are stored in written order, never signature/evaluation order aliases.
// The fixed buffer is copied with the immutable recipe by every tree visitor.
type aclValueExpression struct {
	expressionInfo
	kind      aclValueKind
	args      [4]BoundExpr
	named     bool
	namespace string // Explicit operator qualification, if present.
}

func (n *aclValueExpression) arguments() []BoundExpr { return n.args[:aclValueArity[n.kind]] }
func (n aclValueExpression) baseProperties() ExprProperties {
	p := ExprProperties{}
	if n.kind == aclExplode {
		p.Volatility = Stable
	}
	return p
}
func (b *expressionBinder) bindACLValue(raw parser.FunctionExpr) (BoundExpr, error) {
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := aclDefault
	if strings.EqualFold(name, "makeaclitem") {
		kind = aclMake
	}
	if strings.EqualFold(name, "aclexplode") {
		kind = aclExplode
	}
	types := kind.types()
	var operands [4]comparisonOperand
	var small [4]string
	names := small[:0]
	if len(raw.Args) <= 4 {
		names = small[:len(raw.Args)]
	} else if len(raw.Args) <= 100 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		// The object-kind slot consumes internal CHAR input just like the
		// existing textual function roles. Keep general CHAR casts/storage
		// excluded; in particular, typed TEXT is not an implicit CHAR input.
		o, err := b.stringOperand(child, kind == aclDefault && i == 0)
		if err != nil {
			return nil, err
		}
		if i < 4 {
			operands[i] = o
		}
		if i < len(names) {
			names[i] = "unknown"
			if d := o.descriptor(); d != nil {
				names[i] = b.diagnosticTypeName(d)
			}
			names[i] = functions.NamedArgumentType(raw, i, names[i])
		}
	}
	if err := functions.ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.builtinVisible(raw.Name, functionLookup)
	if err != nil {
		return nil, err
	}
	valid := visible && name == kind.name() && !raw.ResolutionOnly && !raw.Variadic && len(raw.Args) == len(types)
	named := false
	for _, label := range raw.ArgNames {
		if label.Text != "" {
			named = true
			valid = valid && kind == aclExplode && label.Text == "acl"
		}
	}
	if valid {
		for i, t := range types {
			if operands[i].descriptor() != nil {
				if _, err := sqltype.PlanCoercion(operands[i].node.ResultUsage(), b.typ(t).Usage(), sqltype.Implicit); err != nil {
					valid = false
				}
			}
		}
	}
	if !valid {
		return nil, b.functionResolutionErrorForTypes(raw.Name, raw.Pos, names, false)
	}
	if err := functions.CallDecorationError(raw); err != nil {
		return nil, err
	}
	if raw.Star {
		return nil, expressionError("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	n := aclValueExpression{kind: kind, named: named}
	p := n.baseProperties()
	for i, t := range types {
		arg, err := b.coerceConditional(operands[i], b.typ(t).Usage(), name)
		if err != nil {
			return nil, err
		}
		n.args[i] = arg
		p = mergeExpressionProperties(p, arg.Properties())
	}
	result := b.typ(sqltype.ACLItemArray).Usage()
	if kind == aclMake {
		result = b.typ(sqltype.ACLItem).Usage()
	}
	if kind == aclExplode {
		members := []sqltype.CompositeMember{{Attribute: 1, Name: "grantor", Usage: b.typ(sqltype.Oid).Usage()}, {Attribute: 2, Name: "grantee", Usage: b.typ(sqltype.Oid).Usage()}, {Attribute: 3, Name: "privilege_type", Usage: b.typ(sqltype.Text).Usage()}, {Attribute: 4, Name: "is_grantable", Usage: b.typ(sqltype.Bool).Usage()}}
		shape, err := sqltype.RecordShape(members)
		if err != nil {
			return nil, err
		}
		result = shape.Usage()
	}
	n.expressionInfo = b.info(result, raw.Pos, p)
	n.expressionInfo, err = b.builtinRoutineInfo(n.expressionInfo, catalogRoutineProvider, kind.name(), types...)
	if err != nil {
		return nil, err
	}
	return n, nil
}
func (n aclValueExpression) values(c *EvalContext) ([4]sqltype.Value, bool, error) {
	var values [4]sqltype.Value
	if err := evaluationContext(c); err != nil {
		return values, false, err
	}
	null := false
	for i, arg := range n.arguments() {
		v, err := arg.Eval(c)
		if err != nil {
			return values, false, err
		}
		values[i] = v
		null = null || v.IsNull()
	}
	return values, null, nil
}
func (n aclValueExpression) Eval(ctx ExprContext) (sqltype.Value, error) {
	c := evalContext(ctx)
	values, null, err := n.values(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	if n.kind == aclContains {
		ok, err := aclValueContains(c, values[0], values[1])
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Boolean(n.Usage.Type, ok), nil
	}
	if n.kind == aclExplode {
		return sqltype.Value{}, fmt.Errorf("stuntgres: ACL set call requires an invocation")
	}
	scope := c.liveScope()
	if scope == nil {
		return sqltype.Value{}, fmt.Errorf("stuntgres: ACL constructor lacks statement authority")
	}
	authority := &scope.op.session.Engine().aclAuthority
	if n.kind == aclMake {
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

var aclPrivilegeLabels = [...]string{"INSERT", "SELECT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER", "EXECUTE", "USAGE", "CREATE", "TEMPORARY", "CONNECT", "SET", "ALTER SYSTEM", "MAINTAIN"}

func aclPrivilegeNames(text string) (uint32, error) {
	var mask uint32
	for part := range strings.SplitSeq(text, ",") {
		part = strings.Trim(part, " \t\n\r\v\f")
		found := false
		for i, label := range aclPrivilegeLabels {
			if aclPrivilegeEqual(part, label) || i == 10 && aclPrivilegeEqual(part, "TEMP") {
				mask |= 1 << i
				found = true
				break
			}
		}
		if !found {
			return 0, sqlError("22023", `unrecognized privilege type: "`+part+`"`)
		}
	}
	return mask, nil
}
func aclPrivilegeEqual(input, upper string) bool {
	if len(input) != len(upper) {
		return false
	}
	for i := range len(input) {
		c := input[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c != upper[i] {
			return false
		}
	}
	return true
}
func aclDefaultMasks(code string) (uint32, uint32, error) {
	switch code {
	case "c":
		return 0, 0, nil
	case "r":
		return 16511, 0, nil
	case "s":
		return 262, 0, nil
	case "d":
		return 3584, 3072, nil
	case "f":
		return 128, 128, nil
	case "l":
		return 256, 256, nil
	case "L":
		return 6, 0, nil
	case "n":
		return 768, 0, nil
	case "p":
		return 12288, 0, nil
	case "t":
		return 512, 0, nil
	case "F", "S":
		return 256, 0, nil
	case "T":
		return 256, 256, nil
	}
	return 0, 0, sqlError("XX000", "unrecognized object type abbreviation: "+code)
}

// Bound cancellation latency without adding a context dispatch per ACL item.
const aclScanCheckMask = 255

func checkACLValue(c *EvalContext, array sqltype.Value) error {
	if array.ArrayRank() != 1 {
		return sqlError("22023", "ACL arrays must be one-dimensional")
	}
	for i := range array.ArrayLen() {
		if i&aclScanCheckMask == 0 {
			if err := c.Context.Err(); err != nil {
				return err
			}
		}
		v := array.ArrayElement(i)
		if v.IsNull() {
			return sqlError("22004", "ACL arrays must not contain null values")
		}
	}
	scope := c.liveScope()
	if scope == nil {
		return fmt.Errorf("stuntgres: ACL operation lacks statement authority")
	}
	authority := &scope.op.session.Engine().aclAuthority
	for i := range array.ArrayLen() {
		if i&aclScanCheckMask == 0 {
			if err := c.Context.Err(); err != nil {
				return err
			}
		}
		a, _, _, _, _, err := array.ArrayElement(i).ACLItem()
		if err != nil {
			return err
		}
		if a != authority {
			return fmt.Errorf("stuntgres: foreign ACL identity")
		}
	}
	return c.Context.Err()
}
func aclValueContains(c *EvalContext, array, item sqltype.Value) (bool, error) {
	if err := checkACLValue(c, array); err != nil {
		return false, err
	}
	a, g, r, p, o, err := item.ACLItem()
	if err != nil {
		return false, err
	}
	if a != &c.liveScope().op.session.Engine().aclAuthority {
		return false, fmt.Errorf("stuntgres: foreign ACL identity")
	}
	for i := range array.ArrayLen() {
		if i&aclScanCheckMask == 0 {
			if err := c.Context.Err(); err != nil {
				return false, err
			}
		}
		aa, gg, rr, pp, oo, err := array.ArrayElement(i).ACLItem()
		if err != nil {
			return false, err
		}
		if a == aa && g == gg && r == rr && pp&p == p && oo&o == o {
			return true, nil
		}
	}
	return false, c.Context.Err()
}
func (n aclValueExpression) validateStored() error {
	if n.kind > aclContains || n.Usage.Type == nil {
		return fmt.Errorf("stuntgres: invalid stored ACL recipe")
	}
	p := n.baseProperties()
	for i, t := range n.kind.types() {
		arg := n.args[i]
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage().Type.ID() != t {
			return fmt.Errorf("stuntgres: invalid stored ACL signature")
		}
		p = mergeExpressionProperties(p, arg.Properties())
	}
	if n.Property != p {
		return fmt.Errorf("stuntgres: invalid stored ACL properties")
	}
	if n.Usage != n.Usage.Type.Usage() || n.named && n.kind != aclExplode || n.namespace != "" && (n.kind != aclContains || n.namespace != "pg_catalog") {
		return fmt.Errorf("stuntgres: invalid stored ACL result or notation")
	}
	for _, arg := range n.args[len(n.arguments()):] {
		if arg != nil {
			return fmt.Errorf("stuntgres: surplus stored ACL argument")
		}
	}
	result := sqltype.ACLItemArray
	if n.kind == aclMake {
		result = sqltype.ACLItem
	}
	if n.kind == aclContains {
		result = sqltype.Bool
	}
	if n.kind == aclExplode {
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
	} else if n.Usage.Type.ID() != result {
		return fmt.Errorf("stuntgres: invalid stored ACL result")
	}
	return nil
}
func (n aclValueExpression) sql(render func(BoundExpr) (string, error)) (string, error) {
	if err := n.validateStored(); err != nil {
		return "", err
	}
	var parts [4]string
	for i, arg := range n.arguments() {
		text, err := render(arg)
		if err != nil {
			return "", err
		}
		parts[i] = text
	}
	if n.kind == aclContains {
		op := "@>"
		if n.namespace != "" {
			op = "OPERATOR(" + quoteIdentifier(n.namespace) + ".@>)"
		}
		return "(" + parts[0] + " " + op + " " + parts[1] + ")", nil
	}
	if n.named {
		parts[0] = "acl => " + parts[0]
	}
	return "pg_catalog." + n.kind.name() + "(" + strings.Join(parts[:len(n.arguments())], ", ") + ")", nil
}

func containsEmptyACL(value sqltype.Value) bool {
	if value.Type() == nil || value.IsNull() || !value.Type().ContainsACLItem() {
		return false
	}
	physical := value.Type().BaseType()
	if physical.ID() == sqltype.ACLItemArray && value.ArrayRank() == 1 && value.ArrayDimension(0) == (sqltype.ArrayDimension{Lower: 1, Length: 0}) {
		return true
	}
	if physical.IsComposite() {
		for i := range value.CompositeLen() {
			if containsEmptyACL(value.CompositeField(i)) {
				return true
			}
		}
	}
	if physical.IsArray() {
		for i := range value.ArrayLen() {
			if containsEmptyACL(value.ArrayElement(i)) {
				return true
			}
		}
	}
	return false
}
