package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// SessionEndpointKind identifies an endpoint inquiry, never a socket or value.
type SessionEndpointKind uint8

const (
	EndpointClientAddr SessionEndpointKind = iota
	EndpointClientPort
	EndpointServerAddr
	EndpointServerPort
)

var sessionEndpointNames = [...]string{"inet_client_addr", "inet_client_port", "inet_server_addr", "inet_server_port"}

// SessionEndpoint retains only the immutable typed recipe and routine provenance.
type SessionEndpoint struct {
	expr.Info
	Kind SessionEndpointKind
}

func bindSessionEndpoint(b Binder, raw parser.FunctionExpr) (expr.Expr, error) {
	var names []string
	if len(raw.Args) <= 100 && len(raw.Args) != 0 {
		names = make([]string, len(raw.Args))
	}
	for i, child := range raw.Args {
		arg, err := b.AnalyzeArgument(child, AnalysisRequest{Intent: ComparisonArgument})
		if err != nil {
			return nil, err
		}
		if i < len(names) {
			name := "unknown"
			if arg.Usage.Type != nil {
				name = b.DiagnosticTypeName(arg.Usage.Type)
			}
			names[i] = NamedArgumentType(raw, i, name)
		}
	}
	if len(raw.Args) > 100 {
		return nil, expr.ErrorAt("54023", "cannot pass more than 100 arguments to a function", raw.Pos)
	}
	if err := ValidateCallSyntax(b, raw); err != nil {
		return nil, err
	}
	visible, err := b.BuiltinVisible(raw.Name)
	if err != nil {
		return nil, err
	}
	name := raw.Name.Parts[len(raw.Name.Parts)-1].Text
	kind := SessionEndpointKind(len(sessionEndpointNames))
	for i, candidate := range sessionEndpointNames {
		if candidate == name {
			kind = SessionEndpointKind(i)
			break
		}
	}
	if !visible || int(kind) >= len(sessionEndpointNames) || len(raw.Args) != 0 || len(raw.ArgNames) != 0 || raw.Variadic || raw.ResolutionOnly {
		return nil, b.SignatureError(raw.Name, raw.Pos, names)
	}
	if raw.Star {
		return nil, expr.ErrorAt("42809", name+"(*) specified, but "+name+" is not an aggregate function", raw.Pos)
	}
	if err := CallDecorationError(raw); err != nil {
		return nil, err
	}
	info, err := b.BuiltinRoutineInfo(b.Info(b.Type(kind.resultType()).Usage(), raw.Pos, expr.Properties{Volatility: expr.Stable}), CatalogRoutineProvider, name)
	if err != nil {
		return nil, err
	}
	return SessionEndpoint{info, kind}, nil
}

func (k SessionEndpointKind) resultType() sqltype.ID {
	if k == EndpointClientPort || k == EndpointServerPort {
		return sqltype.Int4
	}
	return sqltype.Inet
}

func (n SessionEndpoint) Validate() error {
	if int(n.Kind) >= len(sessionEndpointNames) || n.Usage.Type == nil {
		return fmt.Errorf("stuntgres: invalid session endpoint signature")
	}
	if n.Usage.Type.ID() != n.Kind.resultType() {
		return fmt.Errorf("stuntgres: invalid session endpoint result type")
	}
	return nil
}

func (n SessionEndpoint) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	// In-process sessions have no transport endpoints.
	return sqltype.Null(n.Usage.Type), nil
}

func (n SessionEndpoint) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
