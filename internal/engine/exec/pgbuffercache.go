package exec

import (
	"fmt"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (c *Context) OpenPgBuffercache(n plan.InstalledPgBuffercacheExpr) (expr.SourceCursor, error) {
	if err := EvaluationContext(c); err != nil {
		return expr.SourceCursor{}, err
	}
	view := preparation.DomainCatalog(c)
	if n.Procedure == nil || n.RoutineReference != n.Procedure || n.Ref.Address != n.Procedure.Address() || !preparation.StoredReferenceValid(view, c.RoutineConsumer, n.Ref) {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: invalid installed pg_buffercache reference")
	}
	current, err := ValidateStoredRoutineDescriptor(view, n.Procedure)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if err := extension.ValidatePgBuffercacheIdentity(view, current, n.Ref); err != nil {
		return expr.SourceCursor{}, err
	}
	auth, dependencies, err := RoutineAdmissionAuthority(c)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	if err := CheckRecipeRoutineDescriptor(c, auth, dependencies, n.Procedure); err != nil {
		return expr.SourceCursor{}, err
	}
	current, err = ValidateRoutineDescriptor(auth.Catalog, auth.Roles, n.Procedure)
	if err != nil {
		return expr.SourceCursor{}, err
	}
	n.Ref.Epoch = auth.Catalog.Epoch()
	if err := extension.ValidatePgBuffercacheIdentity(auth.Catalog, current, n.Ref); err != nil {
		return expr.SourceCursor{}, err
	}
	// Invoker authorization deliberately precedes the released execution-time
	// anonymous record/count/type checks, including permission on bad shapes.
	shape := n.Usage.Type
	if shape == nil || !shape.IsComposite() {
		return expr.SourceCursor{}, sqlError("XX000", "return type must be a row type")
	}
	if shape.CompositeLen() != 8 && shape.CompositeLen() != 9 {
		return expr.SourceCursor{}, sqlError("XX000", "incorrect number of output arguments")
	}
	types := [...]sqltype.ID{sqltype.Int4, sqltype.Oid, sqltype.Oid, sqltype.Oid, sqltype.Int2, sqltype.Int8, sqltype.Bool, sqltype.Int2, sqltype.Int4}
	for i := range shape.CompositeLen() {
		actual, ok := auth.Catalog.LookupType(types[i])
		if !ok {
			return expr.SourceCursor{}, fmt.Errorf("stuntgres: missing pg_buffercache field type")
		}
		expected := shape.CompositeMember(i).Usage.Type
		if expected != actual {
			return expr.SourceCursor{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "function return row and query-specified return row do not match", Detail: fmt.Sprintf("Returned type %s at ordinal position %d, but query expects %s.", actual.DiagnosticName(), i+1, expected.DiagnosticName())}
		}
	}
	host := c.LiveHost()
	if host == nil {
		return expr.SourceCursor{}, fmt.Errorf("stuntgres: pg_buffercache lacks statement scope")
	}
	return host.Contrib().PgBuffercache(shape)
}
