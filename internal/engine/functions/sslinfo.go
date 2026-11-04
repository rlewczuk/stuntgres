package functions

import (
	"fmt"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
)

// SSLInfoMember is one released zero-argument connection inquiry. Order is the
// selected subset of sslinfo--1.2.sql, and is immutable installation authority.
type SSLInfoMember struct {
	Name   string
	Result sqltype.ID
}

var SSLInfoMembers = [...]SSLInfoMember{
	{Name: "ssl_client_serial", Result: sqltype.Numeric},
	{Name: "ssl_is_used", Result: sqltype.Bool},
	{Name: "ssl_version", Result: sqltype.Text},
	{Name: "ssl_cipher", Result: sqltype.Text},
	{Name: "ssl_client_cert_present", Result: sqltype.Bool},
	{Name: "ssl_client_dn", Result: sqltype.Text},
	{Name: "ssl_issuer_dn", Result: sqltype.Text},
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
}

func (n SSLInfo) Validate() error {
	member, ok := SSLInfoLookup(n.Name)
	if !ok || !n.Usage.Valid() || n.Usage.Type.ID() != member.Result || n.Usage != n.Usage.Type.Usage() {
		return fmt.Errorf("stuntgres: invalid sslinfo signature")
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
	body, err := n.EnterRoutine(ctx, nil)
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
	value, err := n.Eval(ctx)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	return expr.ScalarSource(value), nil
}
