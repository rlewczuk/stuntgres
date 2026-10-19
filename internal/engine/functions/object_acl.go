package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ObjectACL is pg_get_acl. Args remain in written order; Slots maps them to
// the fixed SQL signature and Labels keep named notation. Evaluation and
// stored rendering retain that order. No result, role read or catalog
// snapshot belongs to this immutable recipe.
type ObjectACL struct {
	expr.Info
	Args   [3]expr.Expr
	Slots  [3]uint8
	Labels [3]string
}

var objectACLLabels = [3]string{"classid", "objid", "objsubid"}
var objectACLTypes = [3]sqltype.ID{sqltype.Oid, sqltype.Oid, sqltype.Int4}

func (n ObjectACL) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	null := false
	for i, arg := range n.Args {
		value, err := arg.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[n.Slots[i]] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	class, err := values[0].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	object, err := values[1].OID()
	if err != nil {
		return sqltype.Value{}, err
	}
	subid, err := values[2].Integer()
	if err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := aclRuntime(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.ObjectACL(ids.ObjectAddress{ClassID: ids.OID(class), ObjectID: ids.OID(object), SubID: int32(subid)}, n.Usage.Type)
}

// Validate checks the stored recipe: slots, labels, argument order and result.
func (n ObjectACL) Validate() error {
	var seen [3]bool
	properties := expr.Properties{Volatility: expr.Stable}
	named := false
	for i, arg := range n.Args {
		slot := n.Slots[i]
		if slot >= 3 || seen[slot] || arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage() != arg.ResultUsage().Type.Usage() || arg.ResultUsage().Type.ID() != objectACLTypes[slot] {
			return fmt.Errorf("stuntgres: invalid stored object ACL signature")
		}
		seen[slot] = true
		if n.Labels[i] != "" {
			if n.Labels[i] != objectACLLabels[slot] {
				return fmt.Errorf("stuntgres: invalid stored object ACL label")
			}
			named = true
		} else if named || int(slot) != i {
			return fmt.Errorf("stuntgres: invalid stored object ACL argument order")
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if n.Usage.Type == nil || n.Usage != n.Usage.Type.Usage() || n.Usage.Type.ID() != sqltype.ACLItemArray || n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored object ACL result")
	}
	return nil
}

func (n ObjectACL) Children() []expr.Expr { return n.Args[:] }

func (n ObjectACL) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("object ACL", children, len(n.Args)); err != nil {
		return nil, err
	}
	n.Info = info
	copy(n.Args[:], children)
	return n, nil
}

// IntrinsicVolatility: the call reads the statement's catalog and roles.
func (n ObjectACL) IntrinsicVolatility() expr.Volatility { return expr.Stable }

// PlanningFold: planning never evaluates catalog inquiries.
func (n ObjectACL) PlanningFold() expr.Fold { return expr.FoldNever }

func (n ObjectACL) LooksLikeFunction() bool { return true }

// Render displays the identifier-quoted call with its labels and implicit
// argument casts.
func (n ObjectACL) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	args := make([]expr.RenderArg, len(n.Args))
	for i, arg := range n.Args {
		args[i] = expr.RenderArg{Expr: arg, Label: n.Labels[i]}
	}
	return expr.Rendering{
		Tree:       fmt.Sprintf("ObjectACL slots %v labels %v", n.Slots, n.Labels),
		Inputs:     n.Args[:],
		Form:       expr.FormCall,
		Name:       "pg_get_acl",
		Quote:      expr.QuoteIdentifier,
		LabelQuote: expr.QuoteVerbatim,
		Args:       args,
	}, nil
}
