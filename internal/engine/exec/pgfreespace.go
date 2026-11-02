package exec

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func (c *Context) pgFreespaceInput(n plan.InstalledPgFreespaceExpr) (storage.FreespaceLayout, int64, bool, error) {
	if err := EvaluationContext(c); err != nil {
		return storage.FreespaceLayout{}, 0, false, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return storage.FreespaceLayout{}, 0, false, fmt.Errorf("stuntgres: invalid installed pg_freespace reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil {
		return storage.FreespaceLayout{}, 0, false, err
	}
	boundary := n.PgFreespace
	boundary.RoutineReference = current
	if err := extension.ValidatePgFreespaceCall(view, current, boundary); err != nil {
		return storage.FreespaceLayout{}, 0, false, err
	}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return storage.FreespaceLayout{}, 0, false, err
	}
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil {
		return storage.FreespaceLayout{}, 0, false, err
	}
	current, err = ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil {
		return storage.FreespaceLayout{}, 0, false, err
	}
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil {
		return storage.FreespaceLayout{}, 0, false, err
	}
	n.Ref.Epoch = auth.Catalog.Epoch()
	n.Procedure, n.RoutineReference = current, current
	if err := extension.ValidatePgFreespaceIdentity(auth.Catalog, current, n.Ref); err != nil {
		return storage.FreespaceLayout{}, 0, false, err
	}
	var values [2]sqltype.Value
	null := false
	for i, child := range n.Args {
		value, err := child.Eval(c)
		if err != nil {
			return storage.FreespaceLayout{}, 0, false, err
		}
		values[n.Slots[i]] = value
		null = null || value.IsNull()
	}
	if null {
		// The SQL wrapper's body formal remains executable under generic
		// planning. A custom body may instead remove its STRICT scalar call;
		// outer spelling does not replace the body's formal-parameter demand.
		if n.Kind.Set() && c.PreserveParameterDemand {
			host := c.LiveHost()
			if host == nil {
				return storage.FreespaceLayout{}, 0, false, fmt.Errorf("stuntgres: missing pg_freespace statement runtime")
			}
			if err := host.Contrib().ValidatePgFreespace(n); err != nil {
				return storage.FreespaceLayout{}, 0, false, err
			}
		}
		return storage.FreespaceLayout{}, 0, true, nil
	}
	oid, err := values[0].ClassOID()
	if err != nil {
		return storage.FreespaceLayout{}, 0, false, err
	}
	var block int64
	if n.Kind.Block() {
		block, err = values[1].Integer()
		if err != nil {
			return storage.FreespaceLayout{}, 0, false, err
		}
	}
	host := c.LiveHost()
	if host == nil {
		return storage.FreespaceLayout{}, 0, false, fmt.Errorf("stuntgres: missing pg_freespace statement runtime")
	}
	result, err := host.Contrib().PgFreespace(n, oid, block)
	return result, block, false, err
}
func (c *Context) EvalPgFreespace(n plan.InstalledPgFreespaceExpr) (sqltype.Value, error) {
	if n.Kind.Set() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: pg_freespace set requires source invocation")
	}
	result, block, null, err := c.pgFreespaceInput(n)
	if err != nil {
		return sqltype.Value{}, err
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	return sqltype.Integer(n.Usage.Type, result.Probe(block))
}
func (c *Context) OpenPgFreespace(n plan.InstalledPgFreespaceExpr) (expr.SourceCursor, error) {
	if !n.Kind.Set() {
		value, err := c.EvalPgFreespace(n)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(value), nil
	}
	result, _, _, err := c.pgFreespaceInput(n)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	// The cursor owns arithmetic state only: no page vector, closure, live host,
	// authority or retained physical root. Each result owns its typed fields.
	return expr.NewSourceCursor(expr.SourceCursor{Type: n.Usage.Type, Stop: result.Blocks, Step: result.LastAvail, Advance: nextPgFreespace}), nil
}
func nextPgFreespace(ctx context.Context, s *expr.SourceCursor) (sqltype.Value, bool, error) {
	if err := ctx.Err(); err != nil {
		return sqltype.Value{}, false, err
	}
	if s.Current >= s.Stop {
		return sqltype.Value{}, false, nil
	}
	fields := make([]sqltype.Value, 2)
	var err error
	fields[0], err = sqltype.Integer(s.Type.CompositeMember(0).Usage.Type, s.Current)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	avail := int64(0)
	if s.Current == s.Stop-1 {
		avail = s.Step
	}
	fields[1], err = sqltype.Integer(s.Type.CompositeMember(1).Usage.Type, avail)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	value, err := sqltype.OwnedCompositeValue(s.Type, fields)
	if err != nil {
		return sqltype.Value{}, false, err
	}
	s.Current++
	return value, true, nil
}
