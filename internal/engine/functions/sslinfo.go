package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// SSLInfoMember is a released declaration. Slots are immutable installation
// authority: certificate inquiries append to the original seven connection slots.
type SSLInfoMember struct {
	Name      string
	Signature string
	Result    sqltype.ID
	Set       bool
}

var SSLInfoMembers = [...]SSLInfoMember{
	{Name: "ssl_client_serial", Result: sqltype.Numeric},
	{Name: "ssl_is_used", Result: sqltype.Bool},
	{Name: "ssl_version", Result: sqltype.Text},
	{Name: "ssl_cipher", Result: sqltype.Text},
	{Name: "ssl_client_cert_present", Result: sqltype.Bool},
	{Name: "ssl_client_dn", Result: sqltype.Text},
	{Name: "ssl_issuer_dn", Result: sqltype.Text},
	{Name: "ssl_client_dn_field", Signature: "25", Result: sqltype.Text},
	{Name: "ssl_issuer_field", Signature: "25", Result: sqltype.Text},
	{Name: "ssl_extension_info", Result: 2249, Set: true},
}

func SSLInfoLookup(name string) (SSLInfoMember, bool) {
	for _, member := range SSLInfoMembers {
		if member.Name == name {
			return member, true
		}
	}
	return SSLInfoMember{}, false
}

// SSLInfo retains a released selector, never transport state or a certificate.
// Embedded sessions have no TLS transport, but the declaration remains volatile.
type SSLInfo struct {
	expr.Info
	Name string
	Arg  expr.Expr
}

func (n SSLInfo) Validate() error {
	member, ok := SSLInfoLookup(n.Name)
	if !ok || !n.Usage.Valid() || n.Usage.Type.ID() != member.Result || n.Usage != n.Usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid sslinfo signature")
	}
	if member.Set {
		if !n.Usage.Type.IsComposite() || n.Usage.Type.CompositeLen() != 3 {
			return fmt.Errorf("stuntgres: invalid sslinfo OUT shape")
		}
		for i, name := range [3]string{"name", "value", "critical"} {
			field := n.Usage.Type.CompositeMember(i)
			if field.Attribute != int32(i+1) || field.Name != name || !field.Usage.Valid() || field.Usage != field.Usage.Type.Usage() || field.Usage.Type.ID() != [3]sqltype.ID{sqltype.Text, sqltype.Text, sqltype.Bool}[i] {
				return fmt.Errorf("stuntgres: invalid sslinfo OUT shape")
			}
		}
	}
	if member.Signature == "25" {
		if n.Arg == nil || !n.Arg.ResultUsage().Valid() || n.Arg.ResultUsage() != n.Arg.ResultUsage().Type.Usage() || n.Arg.ResultUsage().Type.ID() != sqltype.Text {
			return fmt.Errorf("stuntgres: invalid sslinfo argument")
		}
	} else if n.Arg != nil {
		return fmt.Errorf("stuntgres: invalid sslinfo argument")
	}
	return nil
}

func (n SSLInfo) Eval(ctx expr.Context) (sqltype.Value, error) {
	if _, err := evaluationContext(ctx); err != nil {
		return sqltype.Value{}, err
	}
	if err := n.Validate(); err != nil {
		return sqltype.Value{}, err
	}
	if member, _ := SSLInfoLookup(n.Name); member.Set {
		return sqltype.Value{}, fmt.Errorf("stuntgres: sslinfo requires a source invocation")
	}
	var arguments [1]sqltype.Value
	var args []sqltype.Value
	if n.Arg != nil {
		value, err := n.Arg.Eval(ctx)
		if err != nil {
			return sqltype.Value{}, err
		}
		if value.IsNull() {
			return sqltype.Null(n.Usage.Type), nil
		}
		arguments[0], args = value, arguments[:]
	}
	body, err := n.EnterRoutine(ctx, args)
	if err != nil {
		return sqltype.Value{}, err
	}
	defer body.Leave()
	if n.Usage.Type.ID() == sqltype.Bool {
		return sqltype.Boolean(n.Usage.Type, false), nil
	}
	return sqltype.Null(n.Usage.Type), nil
}

func (n SSLInfo) OpenSource(ctx expr.Context) (expr.SourceCursor, error) {
	if member, ok := SSLInfoLookup(n.Name); ok && member.Set {
		if _, err := evaluationContext(ctx); err != nil {
			return expr.SourceCursor{}, err
		}
		if err := n.Validate(); err != nil {
			return expr.SourceCursor{}, err
		}
		body, err := n.EnterRoutine(ctx, nil)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		defer body.Leave()
		return expr.OwnedSource(n.Usage.Type, nil), nil
	}
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
