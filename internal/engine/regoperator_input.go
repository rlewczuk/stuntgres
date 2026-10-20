package engine

import (
	"context"
	"slices"
	"strings"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func collectOperatorGroups(view catalog.Read) exec.OperatorGroups {
	groups := make(exec.OperatorGroups)
	for key, addresses := range view.State().OperatorNames().Buckets {
		for _, address := range addresses {
			d := view.MustOperator(address)
			groups[key.Name] = append(groups[key.Name], d)
		}
	}
	return groups
}

// Earlier namespaces mask only the same ordered operand signature.
func visibleOperators(groups exec.OperatorGroups, path []ids.ObjectAddress, name string) []*catalog.Operator {
	var result []*catalog.Operator
	seen := make(map[analyze.OperatorSignature]bool)
	for _, ns := range path {
		for _, d := range groups[name] {
			if d.Namespace() != ns {
				continue
			}
			signature := analyze.OperatorSignature{Left: d.Left, Right: d.Right}
			if !seen[signature] {
				seen[signature] = true
				result = append(result, d)
			}
		}
	}
	return result
}

type capturedOperatorInput struct {
	types     capturedRegtypeInput
	path      []ids.ObjectAddress
	groups    exec.OperatorGroups
	qualified *[]ids.ObjectAddress
	operands  *[]catalog.ExpressionDependency
}

func (e *Engine) captureOperatorInput(auth AuthorizationView, op *session.Operation) (capturedOperatorInput, error) {
	types, err := e.captureRegtypeInput(auth, op)
	if err != nil {
		return capturedOperatorInput{}, err
	}
	path, err := e.lookupPath(auth, analyze.OperatorLookup)
	if err != nil {
		return capturedOperatorInput{}, err
	}
	return capturedOperatorInput{types: types, path: path, groups: collectOperatorGroups(auth.Catalog)}, nil
}
func (r capturedOperatorInput) OperatorParameter(slot int, empty bool) sqltype.OperatorResolver {
	r.types = r.types.RegtypeParameter(slot, empty).(capturedRegtypeInput)
	return r
}
func (r capturedOperatorInput) numeric(ctx context.Context, text string) (uint32, bool, error) {
	if len(text) == 0 {
		return 0, false, nil
	}
	for i := range text {
		if text[i] < '0' || text[i] > '9' {
			return 0, false, nil
		}
	}
	oid, _ := r.types.auth.Catalog.LookupType(sqltype.Oid)
	v, err := oid.Input(ctx, text, -1)
	if err != nil {
		return 0, true, err
	}
	n, err := v.OID()
	return n, true, err
}
func (r capturedOperatorInput) candidates(text string) ([]*catalog.Operator, error) {
	raw, err := parser.ParseRegclassName(text)
	if err != nil {
		return nil, sqlError("42602", "invalid name syntax")
	}
	parts := raw.Parts
	names := make([]string, len(parts))
	for i, p := range parts {
		names[i] = p.Text
	}
	spelling := strings.Join(names, ".")
	if len(parts) > 3 {
		return nil, sqlError("42601", "improper qualified name (too many dotted names): "+spelling)
	}
	if len(parts) == 3 {
		if parts[0].Text != r.types.database {
			return nil, sqlError("0A000", "cross-database references are not implemented: "+spelling)
		}
		parts = parts[1:]
	}
	path := r.path
	if len(parts) == 2 {
		ns := r.types.auth.Catalog.NameAddress(ids.NameKey{ClassID: ids.NamespaceClassID, Name: parts[0].Text})
		if parts[0].Text == "pg_temp" {
			ns = r.types.temporary
		}
		if ns == (ids.ObjectAddress{}) {
			return nil, nil
		}
		if err := r.types.auth.CanUse(r.types.auth.Ref(ns)); err != nil {
			return nil, err
		}
		if r.qualified != nil && !slices.Contains(*r.qualified, ns) {
			*r.qualified = append(*r.qualified, ns)
		}
		path = []ids.ObjectAddress{ns}
	}
	return visibleOperators(r.groups, path, parts[len(parts)-1].Text), nil
}
func (r capturedOperatorInput) ResolveRegoper(ctx context.Context, text string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if n, ok, err := r.numeric(ctx, text); ok {
		return n, err
	}
	candidates, err := r.candidates(text)
	if err != nil {
		return 0, err
	}
	if len(candidates) == 0 {
		return 0, sqlError("42883", "operator does not exist: "+text)
	}
	if len(candidates) > 1 {
		return 0, sqlError("42725", "more than one operator named "+text)
	}
	return uint32(candidates[0].Address().ObjectID), nil
}
func (r capturedOperatorInput) ResolveRegoperator(ctx context.Context, text string) (uint32, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if n, ok, err := r.numeric(ctx, text); ok {
		return n, err
	}
	var name string
	var oids [2]ids.OID
	count := 0
	types := r.types
	types.qualified = r.qualified
	types.resolved = r.operands
	err := parser.ParseRegOperatorSignature(text, func(raw string) error {
		name = raw
		// Split syntax precedes the right delimiter; semantic namespace checks
		// still follow resolution of every operand.
		if _, err := parser.ParseRegclassName(name); err != nil {
			return sqlError("42602", "invalid name syntax")
		}
		return nil
	}, func(arg string) error {
		var id uint32
		if !strings.EqualFold(arg, "NONE") {
			var err error
			id, err = types.resolveTypeName(ctx, arg)
			if err != nil {
				return err
			}
		}
		// PostgreSQL resolves even the first over-limit operand before checking
		// FUNC_MAX_ARGS. Only the first two identities are needed for lookup.
		if count >= 100 {
			return sqlError("54023", "too many arguments")
		}
		if count < len(oids) {
			oids[count] = ids.OID(id)
		}
		count++
		return nil
	})
	if err != nil {
		return 0, err
	}
	if count != 2 {
		d := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "54023", Message: "too many arguments", Hint: "Provide two argument types for operator."}
		if count == 1 {
			d.Code = "42P02"
			d.Message = "missing argument"
			d.Hint = "Use NONE to denote the missing argument of a unary operator."
		}
		return 0, d
	}
	candidates, err := r.candidates(name)
	if err != nil {
		return 0, err
	}
	for _, d := range candidates {
		if d.Left == oids[0] && d.Right == oids[1] {
			return uint32(d.Address().ObjectID), nil
		}
	}
	return 0, sqlError("42883", "operator does not exist: "+text)
}
