package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// AccessInquiry is pg_has_role (Count 2 or 3) or row_security_active
// (RowSecurity, Count 1). It retains only its typed recipe: all authority is
// borrowed from the executing statement through ACLRuntime, never cached on
// the expression or statement. Args beyond Count are nil.
type AccessInquiry struct {
	expr.Info
	RowSecurity bool
	Args        [3]expr.Expr
	Count       int
}

func (n AccessInquiry) Name() string {
	if n.RowSecurity {
		return "row_security_active"
	}
	return "pg_has_role"
}

func (n AccessInquiry) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [3]sqltype.Value
	null := false
	for i := range n.Count {
		value, err := n.Args[i].Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		values[i] = value
		null = null || value.IsNull()
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	runtime, err := aclRuntime(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	var result bool
	if n.RowSecurity {
		relation := AccessRelation{ByOID: n.Args[0].ResultUsage().Type.ID() == sqltype.Oid}
		if relation.ByOID {
			relation.OID, err = values[0].OID()
		} else {
			relation.Name, err = values[0].Text()
		}
		if err != nil {
			return sqltype.Value{}, err
		}
		result, err = runtime.RowSecurityActive(relation)
	} else {
		var actor *AccessRole
		targetIndex := 0
		if n.Count == 3 {
			role, failure := accessRoleValue(n.Args[0], values[0])
			if failure != nil {
				return sqltype.Value{}, failure
			}
			actor, targetIndex = &role, 1
		}
		target, failure := accessRoleValue(n.Args[targetIndex], values[targetIndex])
		if failure != nil {
			return sqltype.Value{}, failure
		}
		privilege, failure := values[n.Count-1].Text()
		if failure != nil {
			return sqltype.Value{}, failure
		}
		result, err = runtime.RoleMembership(actor, target, privilege)
	}
	if err != nil {
		return sqltype.Value{}, err
	}
	return sqltype.Boolean(n.Usage.Type, result), nil
}

func accessRoleValue(arg expr.Expr, value sqltype.Value) (AccessRole, error) {
	var role AccessRole
	var err error
	if arg.ResultUsage().Type.ID() == sqltype.Oid {
		role.ByOID = true
		role.OID, err = value.OID()
	} else {
		role.Name, err = value.Text()
	}
	return role, err
}

// Validate checks the stored recipe: arity, formals and result.
func (n AccessInquiry) Validate() error {
	if n.RowSecurity && n.Count != 1 || !n.RowSecurity && n.Count != 2 && n.Count != 3 {
		return fmt.Errorf("stuntgres: invalid stored access inquiry arity")
	}
	properties := expr.Properties{Volatility: expr.Stable}
	for i, arg := range n.Args {
		if i >= n.Count {
			if arg != nil {
				return fmt.Errorf("stuntgres: invalid stored access inquiry tail")
			}
			continue
		}
		if arg == nil || arg.ResultUsage().Type == nil || arg.ResultUsage() != arg.ResultUsage().Type.Usage() {
			return fmt.Errorf("stuntgres: invalid stored access inquiry argument")
		}
		id := arg.ResultUsage().Type.ID()
		valid := id == sqltype.Text
		if !n.RowSecurity && i < n.Count-1 {
			valid = id == sqltype.Name || id == sqltype.Oid
		} else if n.RowSecurity {
			valid = id == sqltype.Text || id == sqltype.Oid
		}
		if !valid {
			return fmt.Errorf("stuntgres: invalid stored access inquiry signature")
		}
		properties = expr.MergeProperties(properties, arg.Properties())
	}
	if n.Usage.Type == nil || n.Usage != n.Usage.Type.Usage() || n.Usage.Type.ID() != sqltype.Bool || n.Properties() != properties {
		return fmt.Errorf("stuntgres: invalid stored access inquiry result")
	}
	return nil
}

func (n AccessInquiry) Children() []expr.Expr { return n.Args[:n.Count] }

func (n AccessInquiry) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if n.Count < 0 || n.Count > len(n.Args) {
		return nil, fmt.Errorf("stuntgres: invalid stored access inquiry arity")
	}
	if err := checkChildren("access inquiry", children, n.Count); err != nil {
		return nil, err
	}
	n.Info = info
	copy(n.Args[:], children)
	return n, nil
}

// IntrinsicVolatility: the inquiries read the statement's roles and catalog.
func (n AccessInquiry) IntrinsicVolatility() expr.Volatility { return expr.Stable }

// PlanningFold: planning never evaluates role or row-security inquiries.
func (n AccessInquiry) PlanningFold() expr.Fold { return expr.FoldNever }

func (n AccessInquiry) LooksLikeFunction() bool { return true }

// PreflightFold: preparation only preflights the children.
func (n AccessInquiry) PreflightFold() expr.Preflight { return expr.PreflightChildren }

// SameGroup implements expr.GroupIdentity.
func (n AccessInquiry) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(AccessInquiry)
	return ok && n.RowSecurity == o.RowSecurity && n.Count == o.Count && sameChildren(n.Children(), o.Children(), same)
}

// Render displays the call with its implicit argument casts.
func (n AccessInquiry) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	children := n.Children()
	args := make([]expr.RenderArg, len(children))
	for i, arg := range children {
		args[i] = expr.RenderArg{Expr: arg}
	}
	return expr.Rendering{
		Tree:   "AccessInquiry " + n.Name(),
		Inputs: children,
		Form:   expr.FormCall,
		Name:   n.Name(),
		Args:   args,
	}, nil
}

func (e AccessInquiry) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind != expr.CaptureFunction {
		return expr.CaptureOperands{}, false, nil
	}
	return captureShape(e.Count, len(e.Args))
}

func (e AccessInquiry) CaptureOperand(written int) (expr.Expr, error) {
	return captureAt(e.Args[:], e.Count, written)
}
