package functions

import (
	"fmt"
	"strings"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type IdentityKind uint8

const (
	IdentityCatalog IdentityKind = iota
	IdentityDatabase
	IdentityRole
	IdentityCurrentUser
	IdentitySessionUser
	IdentityUser
	IdentitySystemUser
	IdentityCurrentSchema
)

func (k IdentityKind) Spelling() string {
	return [...]string{"current_catalog", "current_database", "current_role", "current_user", "session_user", "user", "system_user", "current_schema"}[k]
}

// Identity retains keyword versus ordinary-call syntax independently of the
// execution's captured principal. Kind and OrdinaryCall are immutable recipe data.
type Identity struct {
	expr.Info
	Kind         IdentityKind
	OrdinaryCall bool
}

func (n Identity) ResultNullability(expr.ResultSite) (bool, bool) {
	if n.Kind == IdentityCurrentSchema {
		return true, true
	}
	return false, false
}
func (n Identity) Eval(c expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(c); err != nil {
		return sqltype.Value{}, err
	}
	runtime, err := sessionInquiryRuntime(c)
	if err != nil {
		return sqltype.Value{}, err
	}
	return runtime.IdentityValue(n.Kind, n.Usage.Type)
}
func BindIdentity(b Binder, name string, pos parser.Position) (expr.Expr, error) {
	var kind IdentityKind
	switch name {
	case "current_catalog":
		kind = IdentityCatalog
	case "current_database":
		kind = IdentityDatabase
	case "current_role":
		kind = IdentityRole
	case "current_user":
		kind = IdentityCurrentUser
	case "session_user":
		kind = IdentitySessionUser
	case "user":
		kind = IdentityUser
	case "system_user":
		kind = IdentitySystemUser
	case "current_schema":
		kind = IdentityCurrentSchema
	default:
		return nil, fmt.Errorf("stuntgres: invalid identity keyword")
	}
	id := sqltype.Name
	if kind == IdentitySystemUser {
		id = sqltype.Text
	}
	info := b.Info(b.Type(id).Usage(), pos, expr.Properties{Volatility: expr.Stable})
	if kind == IdentitySystemUser {
		var err error
		info, err = b.BuiltinRoutineInfo(info, CatalogRoutineProvider, "system_user")
		if err != nil {
			return nil, err
		}
	}
	return Identity{Info: info, Kind: kind}, nil
}
func bindIdentityFunction(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) != 0 {
		var err error
		names, err = ArgumentTypeNames(b, raw)
		if err != nil {
			return nil, err
		}
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	valid := name == "current_database" || name == "current_user" || name == "session_user" || name == "system_user" || name == "current_schema"
	if !visible || !valid || len(raw.Args) != 0 || raw.Variadic {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	node, err := BindIdentity(b, name, raw.Pos)
	if err != nil {
		return nil, err
	}
	identity := node.(Identity)
	identity.OrdinaryCall = true
	if identity.RoutineReference == nil {
		identity.Info, err = b.BuiltinRoutineInfo(identity.Info, CatalogRoutineProvider, name)
		if err != nil {
			return nil, err
		}
	}
	return identity, nil
}
func (n Identity) Validate() error {
	if n.Kind > IdentityCurrentSchema {
		return fmt.Errorf("stuntgres: invalid deparse identity kind")
	}
	if n.OrdinaryCall {
		switch n.Kind {
		case IdentityDatabase, IdentityCurrentUser, IdentitySessionUser, IdentitySystemUser, IdentityCurrentSchema:
		default:
			return fmt.Errorf("stuntgres: invalid deparse identity call")
		}
	} else if n.Kind == IdentityDatabase {
		return fmt.Errorf("stuntgres: invalid deparse identity keyword")
	}
	typ := sqltype.Name
	if n.Kind == IdentitySystemUser {
		typ = sqltype.Text
	}
	if n.Usage.Type == nil || n.Usage.Type.ID() != typ || n.Usage != n.Usage.Type.Usage() || n.Property != (expr.Properties{Volatility: expr.Stable}) || n.UnknownNull {
		return fmt.Errorf("stuntgres: invalid deparse identity metadata")
	}
	return nil
}
func (n Identity) Children() []expr.Expr { return nil }
func (n Identity) Rebuild(info expr.Info, children []expr.Expr) (expr.Expr, error) {
	if err := checkChildren("identity", children, 0); err != nil {
		return nil, err
	}
	n.Info = info
	return n, nil
}

// Keep the historical analyzed-property fallback, including eliminated arms.
func (n Identity) IntrinsicVolatility() expr.Volatility { return n.Property.Volatility }
func (Identity) PlanningFold() expr.Fold                { return expr.FoldNever }
func (Identity) PreflightFold() expr.Preflight          { return expr.PreflightKeep }
func (Identity) GroupLeaf()                             {}
func (Identity) LooksLikeFunction() bool                { return true }
func (n Identity) SameGroup(other expr.Expr, same func(expr.Expr, expr.Expr) bool, definition bool) bool {
	o, ok := other.(Identity)
	return ok && n.Kind == o.Kind && (!definition || n.OrdinaryCall == o.OrdinaryCall)
}
func (n Identity) Render() (expr.Rendering, error) {
	if err := n.Validate(); err != nil {
		return expr.Rendering{}, err
	}
	name := n.Kind.Spelling()
	spec := expr.Rendering{Tree: fmt.Sprintf("Identity kind %d ordinaryCall %t", n.Kind, n.OrdinaryCall), Form: expr.FormLeaf, Text: strings.ToUpper(name)}
	spec.Replay.Text = name
	if n.OrdinaryCall {
		spec.Form, spec.Name, spec.Text = expr.FormCall, name, ""
		spec.Quote = expr.QuoteIdentifier
		spec.Replay.Text = ""
	}
	return spec, nil
}
func (n Identity) Capture(kind expr.CaptureKind) (expr.CaptureOperands, bool, error) {
	if kind == expr.CaptureIdentity && !n.OrdinaryCall || kind == expr.CaptureFunction && n.OrdinaryCall {
		return expr.CaptureOperands{}, true, nil
	}
	return expr.CaptureOperands{}, false, nil
}
