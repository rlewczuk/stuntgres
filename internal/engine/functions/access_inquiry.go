package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// AccessKind selects one typed access inquiry recipe.
type AccessKind uint8

const (
	AccessMembership AccessKind = iota
	AccessRowSecurity
	AccessFunction
	AccessLanguage
	AccessForeignWrapper
	AccessServer
	AccessDatabase
	AccessSchema
	AccessType
	AccessTablespace
	AccessTable
	AccessSequence
	AccessColumnPrivilege
	AccessAnyColumn
	AccessLargeObject
	AccessParameter
)

// AccessInquiry retains only typed operands, never authority or decisions.
type AccessInquiry struct {
	expr.Info
	Kind  AccessKind
	Args  [4]expr.Expr
	Count int
}

func (AccessInquiry) ResultNullability(expr.ResultSite) (nullable, known bool) {
	return true, true
}

func (n AccessInquiry) Name() string {
	switch n.Kind {
	case AccessMembership:
		return "pg_has_role"
	case AccessRowSecurity:
		return "row_security_active"
	case AccessFunction:
		return "has_function_privilege"
	case AccessLanguage:
		return "has_language_privilege"
	case AccessForeignWrapper:
		return "has_foreign_data_wrapper_privilege"
	case AccessServer:
		return "has_server_privilege"
	case AccessDatabase:
		return "has_database_privilege"
	case AccessSchema:
		return "has_schema_privilege"
	case AccessType:
		return "has_type_privilege"
	case AccessTablespace:
		return "has_tablespace_privilege"
	case AccessTable:
		return "has_table_privilege"
	case AccessSequence:
		return "has_sequence_privilege"
	case AccessColumnPrivilege:
		return "has_column_privilege"
	case AccessAnyColumn:
		return "has_any_column_privilege"
	case AccessLargeObject:
		return "has_largeobject_privilege"
	case AccessParameter:
		return "has_parameter_privilege"
	}
	return ""
}

func (n AccessInquiry) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	var values [4]sqltype.Value
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
	if n.Kind == AccessRowSecurity {
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
		if n.Count == n.minimumArity()+1 {
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
		if n.Kind == AccessMembership {
			result, err = runtime.RoleMembership(actor, target, privilege)
		} else {
			var exists bool
			if n.relationInquiry() {
				relation := AccessRelation{ByOID: target.ByOID, OID: target.OID, Name: target.Name}
				var column AccessColumn
				if n.Kind == AccessColumnPrivilege {
					i := n.Count - 2
					column.ByNumber = n.Args[i].ResultUsage().Type.ID() == sqltype.Int2
					if column.ByNumber {
						number, failure := values[i].Integer()
						if failure != nil {
							return sqltype.Value{}, failure
						}
						column.Number = int32(number)
					} else {
						column.Name, err = values[i].Text()
						if err != nil {
							return sqltype.Value{}, err
						}
					}
				}
				result, exists, err = runtime.RelationPrivilege(n.Kind, actor, relation, column, privilege)
			} else {
				result, exists, err = runtime.ObjectPrivilege(n.Kind, actor, target, privilege)
			}
			if err == nil && !exists {
				return sqltype.Null(n.Usage.Type), nil
			}
		}
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
	if !n.validArity() {
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
		preferred, alternate := n.formalAxis(i)
		valid := id == preferred || alternate != 0 && id == alternate
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

func (n AccessInquiry) relationInquiry() bool {
	switch n.Kind {
	case AccessTable, AccessSequence, AccessColumnPrivilege, AccessAnyColumn:
		return true
	}
	return false
}

func (n AccessInquiry) minimumArity() int {
	if n.Kind == AccessRowSecurity {
		return 1
	}
	if n.Kind == AccessColumnPrivilege {
		return 3
	}
	return 2
}

func (n AccessInquiry) validArity() bool {
	switch n.Kind {
	case AccessRowSecurity:
		return n.Count == 1
	case AccessColumnPrivilege:
		return n.Count == 3 || n.Count == 4
	case AccessMembership, AccessFunction, AccessLanguage, AccessForeignWrapper,
		AccessServer, AccessDatabase, AccessSchema, AccessType, AccessTablespace,
		AccessTable, AccessSequence, AccessAnyColumn, AccessLargeObject, AccessParameter:
		return n.Count == 2 || n.Count == 3
	}
	return false
}

// formalAxis gives the released preferred unknown type and optional alternative.
func (n AccessInquiry) formalAxis(i int) (sqltype.ID, sqltype.ID) {
	if n.Kind == AccessRowSecurity {
		return sqltype.Text, sqltype.Oid
	}
	if i == n.Count-1 {
		return sqltype.Text, 0
	}
	if n.Kind == AccessColumnPrivilege && i == n.Count-2 {
		return sqltype.Text, sqltype.Int2
	}
	if n.Kind != AccessMembership && i == n.Count-n.minimumArity() {
		if n.Kind == AccessLargeObject {
			return sqltype.Oid, 0
		}
		if n.Kind == AccessParameter {
			return sqltype.Text, 0
		}
		return sqltype.Text, sqltype.Oid
	}
	return sqltype.Name, sqltype.Oid
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

// PlanningFold: planning never evaluates access inquiries.
func (n AccessInquiry) PlanningFold() expr.Fold { return expr.FoldNever }

func (n AccessInquiry) LooksLikeFunction() bool { return true }

// PreflightFold: preparation only preflights the children.
func (n AccessInquiry) PreflightFold() expr.Preflight { return expr.PreflightChildren }

// SameGroup implements expr.GroupIdentity.
func (n AccessInquiry) SameGroup(other expr.Expr, same func(a, b expr.Expr) bool, definition bool) bool {
	o, ok := other.(AccessInquiry)
	return ok && n.Kind == o.Kind && n.Count == o.Count && sameChildren(n.Children(), o.Children(), same)
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
