package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// BindRestoredDomainDefault binds a CREATE DOMAIN default from the trusted
// Restore snapshot its operation carries, and reports false without one. It
// is reached only from the paired CREATE's existing stage host, with
// Engine.mu held. The capture is operation-local and expires when that
// statement returns; a SQL comment alone grants no engine authority.
func (h TypeHost) BindRestoredDomainDefault(a ddl.Authority, d *catalog.Type, raw parser.Expr, input sqltype.InputEnvironment, output sqltype.OutputEnvironment) (bool, error) {
	if h.Op == nil {
		return false, nil
	}
	capture := core.OperationReplayOrZero(h.Op).DomainDefault
	if capture == nil {
		return false, nil
	}
	fail := func() (bool, error) {
		return true, fmt.Errorf("stuntgres: invalid trusted domain default snapshot authority")
	}
	auth := core.DDLAuthorityOf(a)
	if !core.PartitionConstraintReplayImport(h.E, h.Op, auth) || capture.Statement == nil {
		return fail()
	}
	if _, ok := h.E.ImportTargets[auth.State.CurrentUser]; !ok {
		return fail()
	}
	r := capture.Record
	if err := r.Validate(); err != nil {
		return true, err
	}
	namespace, ok := auth.Catalog.Namespace(d.Namespace())
	if !ok || namespace.Temporary != (ids.SessionToken{}) || namespace.Name() != r.Schema || d.Name() != r.Domain || len(capture.Statement.Name.Parts) != 2 || capture.Statement.Name.Parts[0].Text != r.Schema || capture.Statement.Name.Parts[1].Text != r.Domain {
		return fail()
	}
	d.DomainDefault, d.DomainDefaultOwn, d.DomainDefaultText = nil, false, ""
	if r.Absent {
		literal, ok := raw.(parser.LiteralExpr)
		if !ok || literal.Value.Kind != parser.NullValue {
			return fail()
		}
		return true, nil
	}
	usage, err := typedef.SnapshotUsage(h, a, r)
	if err != nil {
		return true, err
	}
	if r.Own && usage != d.SQLType.DomainBase() {
		return fail()
	}
	if _, err := sqltype.PlanCoercion(usage, d.SQLType.DomainBase(), sqltype.Assignment); err != nil {
		return true, err
	}
	if err := h.E.BindDomainDefaultUsage(h.Op.Context(), h.Op, auth, h.CatalogStage, d, raw, usage, input, output); err != nil {
		return true, err
	}
	if d.DomainDefault == nil || d.DomainDefault.Expression.ResultUsage() != usage {
		return fail()
	}
	d.DomainDefaultOwn = r.Own
	return true, nil
}
