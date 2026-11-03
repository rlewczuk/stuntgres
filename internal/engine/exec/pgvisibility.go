package exec

import (
	"fmt"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (c *Context) pgvisibilityInput(n *plan.InstalledPgvisibilityExpr) (uint32, int64, bool, error) {
	if err := EvaluationContext(c); err != nil {
		return 0, 0, false, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return 0, 0, false, fmt.Errorf("stuntgres: invalid installed pg_visibility reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil {
		return 0, 0, false, err
	}
	boundary := n.Pgvisibility
	boundary.RoutineReference = current
	if err := extension.ValidatePgvisibilityCall(view, current, boundary); err != nil {
		return 0, 0, false, err
	}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return 0, 0, false, err
	}
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil {
		return 0, 0, false, err
	}
	current, err = ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil {
		return 0, 0, false, err
	}
	if err := auth.Check(access.Requirement{Object: auth.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil {
		return 0, 0, false, err
	}
	n.Ref.Epoch = auth.Catalog.Epoch()
	n.Procedure, n.RoutineReference = current, current
	var values [2]sqltype.Value
	null := false
	for i, child := range n.Args {
		value, err := child.Eval(c)
		if err != nil {
			return 0, 0, false, err
		}
		values[n.Slots[i]] = value
		null = null || value.IsNull()
	}
	if null {
		return 0, 0, true, nil
	}
	oid, err := values[0].ClassOID()
	if err != nil {
		return 0, 0, false, err
	}
	var block int64
	if n.Kind.Block() {
		block, err = values[1].Integer()
		if err != nil {
			return 0, 0, false, err
		}
	}
	if c.LiveHost() == nil {
		return 0, 0, false, fmt.Errorf("stuntgres: missing pg_visibility statement runtime")
	}
	return oid, block, false, nil
}
func pgvisibilityRecord(d *sqltype.Descriptor, kind functions.PgvisibilityKind, counts [2]int64, block int64, bits storage.VisibilityBits, pd bool) (sqltype.Value, error) {
	fields := make([]sqltype.Value, d.CompositeLen())
	if kind == functions.VisibilityMapSummary {
		var err error
		fields[0], err = sqltype.Integer(d.CompositeMember(0).Usage.Type, counts[0])
		if err != nil {
			return sqltype.Value{}, err
		}
		fields[1], err = sqltype.Integer(d.CompositeMember(1).Usage.Type, counts[1])
		if err != nil {
			return sqltype.Value{}, err
		}
	} else {
		offset := 0
		if kind.Set() {
			var err error
			fields[0], err = sqltype.Integer(d.CompositeMember(0).Usage.Type, block)
			if err != nil {
				return sqltype.Value{}, err
			}
			offset = 1
		}
		fields[offset] = sqltype.Boolean(d.CompositeMember(offset).Usage.Type, bits.AllVisible)
		fields[offset+1] = sqltype.Boolean(d.CompositeMember(offset+1).Usage.Type, bits.AllFrozen)
		if kind.Page() {
			fields[offset+2] = sqltype.Boolean(d.CompositeMember(offset+2).Usage.Type, pd)
		}
	}
	return sqltype.OwnedCompositeValue(d, fields)
}
func (c *Context) EvalPgvisibility(n plan.InstalledPgvisibilityExpr) (sqltype.Value, error) {
	if n.Kind.Set() {
		return sqltype.Value{}, fmt.Errorf("stuntgres: pg_visibility set requires source invocation")
	}
	oid, block, null, err := c.pgvisibilityInput(&n)
	if err != nil {
		return sqltype.Value{}, err
	}
	if null {
		return sqltype.Null(n.Usage.Type), nil
	}
	if n.Kind == functions.VisibilityTruncateMap {
		if err := c.LiveHost().Contrib().PgvisibilityTruncate(n, oid); err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.VoidValue(n.Usage.Type), nil
	}
	result, err := c.LiveHost().Contrib().Pgvisibility(n, oid, block)
	if err != nil {
		return sqltype.Value{}, err
	}
	var bits storage.VisibilityBits
	if n.Kind != functions.VisibilityMapSummary {
		bits = result.Probe(block)
	}
	pd := bits.AllVisible
	if result.PageRuns != nil {
		pd = result.PageVisible(block)
	}
	return pgvisibilityRecord(n.Usage.Type, n.Kind, [2]int64{result.AllVisible, result.AllFrozen}, block, bits, pd)
}
func (c *Context) OpenPgvisibility(n plan.InstalledPgvisibilityExpr) (expr.SourceCursor, error) {
	if !n.Kind.Set() {
		value, err := c.EvalPgvisibility(n)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		return expr.ScalarSource(value), nil
	}
	oid, _, null, err := c.pgvisibilityInput(&n)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if null {
		return expr.OwnedSource(n.Usage.Type, nil), nil
	}
	if n.Kind.Check() {
		findings, err := c.LiveHost().Contrib().PgvisibilityCheck(n, oid)
		if err != nil {
			return expr.SourceCursor{}, err
		}
		values := make([]sqltype.Value, len(findings))
		for i, version := range findings {
			if err := c.Context.Err(); err != nil {
				return expr.SourceCursor{}, err
			}
			block, offset, err := storage.LogicalTID(version)
			if err != nil {
				return expr.SourceCursor{}, err
			}
			values[i], err = sqltype.TIDValue(n.Usage.Type, block, offset)
			if err != nil {
				return expr.SourceCursor{}, err
			}
		}
		return expr.OwnedSource(n.Usage.Type, values), nil
	}
	result, err := c.LiveHost().Contrib().Pgvisibility(n, oid, 0)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if result.Blocks > int64(^uint(0)>>1) {
		return expr.SourceCursor{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22003", Message: "bigint out of range"}
	}
	values := make([]sqltype.Value, int(result.Blocks))
	run, pageRun := 0, 0
	for i := range values {
		if err := c.Context.Err(); err != nil {
			return expr.SourceCursor{}, err
		}
		for int64(i) >= result.Runs[run].End {
			run++
		}
		bits := result.Runs[run].Bits
		pd := bits.AllVisible
		if result.PageRuns != nil {
			for int64(i) >= result.PageRuns[pageRun].End {
				pageRun++
			}
			pd = result.PageRuns[pageRun].Bits.AllVisible
		}
		values[i], err = pgvisibilityRecord(n.Usage.Type, n.Kind, [2]int64{}, int64(i), bits, pd)
		if err != nil {
			return expr.SourceCursor{}, err
		}
	}
	return expr.OwnedSource(n.Usage.Type, values), nil
}
