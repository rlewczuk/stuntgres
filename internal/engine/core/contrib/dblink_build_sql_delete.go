package contrib

import (
	"context"
	"fmt"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/extension"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/regobj"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func dblinkDeleteAuthority[A Authority](auth A, n plan.InstalledDblinkBuildSQLDeleteExpr) error {
	view := auth.Access()
	if n.Procedure == nil || n.CallRoutineReference() != n.Procedure || n.Ref.Domain() != view.Catalog.Owner().Domain || n.Ref.Epoch != view.Catalog.Epoch() {
		return ids.ErrCatalogReplaced
	}
	current, ok := view.Catalog.Procedure(n.Ref.Address)
	if !ok || !exec.SameRoutineDeclaration(current, n.Procedure) { return ids.ErrCatalogReplaced }
	if err := extension.ValidateDblinkIdentity(view.Catalog, current, n.Ref); err != nil { return err }
	boundary := n.DblinkBuildSQLDelete
	boundary.RoutineReference = current
	if err := extension.ValidateDblinkBuildSQLDeleteCall(view.Catalog, current, boundary); err != nil { return err }
	if err := auth.Check(access.Requirement{Object: view.Ref(current.Namespace()), Privileges: authorization.UsagePrivilege}); err != nil { return err }
	return auth.Check(access.Requirement{Object: n.Ref, Privileges: authorization.ExecutePrivilege})
}

// DblinkBuildSQLDelete captures only the invocation's catalog metadata. It never
// opens relation storage or retains authority, locks, hosts or physical handles.
func DblinkBuildSQLDelete[A Authority](h Host[A], n plan.InstalledDblinkBuildSQLDeleteExpr, values [4]sqltype.Value) (string, error) {
	if err := h.Active(); err != nil { return "", err }
	h.ReacquireOwnerEngine()
	auth, err := h.RoutineAuthorizationLocked()
	if err == nil { err = dblinkDeleteAuthority(auth, n) }
	var path []ids.ObjectAddress
	if err == nil { path, err = auth.ReferencePath() }
	h.ReleaseOwnerEngineLocked()
	if err != nil { return "", err }
	text, err := values[0].Text()
	if err != nil { return "", err }
	oid, err := h.ClassInput(auth, path).ResolveRelationName(h.Operation().Context(), text)
	if err != nil { return "", err }
	epoch := auth.Access().Catalog.Epoch()
	address := ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: ids.OID(oid)}
	target, ok := auth.Access().Catalog.Relation(address)
	if !ok { return "", ids.ErrCatalogReplaced }
	ref := h.CatalogDomain().Ref(epoch, address)
	var contribution lock.RelationContribution
	defer h.LockManager().ReleaseRelation(&contribution)
	if err := h.AcquireRelationContribution(ref, lock.AccessShare, &contribution); err != nil { return "", err }

	// A lock wait can cross catalog or ACL publication. Re-admit the exact
	// incarnation and current routine/table authority before using any metadata.
	h.ReacquireOwnerEngine()
	auth, err = h.RoutineAuthorizationLocked()
	if err == nil && (auth.Access().Catalog.Epoch() != epoch || auth.Access().Catalog.Get(address) != target) { err = ids.ErrCatalogReplaced }
	if err == nil { err = dblinkDeleteAuthority(auth, n) }
	if err == nil {
		// table_openrv rejects these kinds before checking relation SELECT.
		// Do not import the statistics functions' broader kind restrictions.
		detail := ""
		switch target.RelationKind {
		case catalog.IndexRelation:
			detail = "This operation is not supported for indexes."
			if target.SQLIndex != nil {
				if parent, ok := auth.Access().Catalog.Relation(target.SQLIndex.Parent); ok && parent.Partition != nil {
					detail = "This operation is not supported for partitioned indexes."
				}
			}
		case catalog.CompositeRelation:
			detail = "This operation is not supported for composite types."
		}
		if detail != "" {
			err = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: fmt.Sprintf(`cannot open relation "%s"`, target.Name()), Detail: detail}
		}
	}
	if err == nil { err = auth.Check(access.Requirement{Object: ref, Privileges: authorization.SelectPrivilege}) }
	var schema string
	qualified := false
	if err == nil {
		path, err = auth.ReferencePath()
		if err == nil {
			qualified = !regobj.NamedObjectVisibleAt(auth.Access().Catalog, nil, path, target)
			if qualified {
				namespace, exists := auth.Access().Catalog.Namespace(target.Namespace())
				if !exists { err = ids.ErrCatalogReplaced } else { schema = namespace.Name() }
			}
		}
	}
	if err == nil { h.MarkTemporaryTargetLocked(ref) }
	h.ReleaseOwnerEngineLocked()
	if err != nil { return "", err }
	_, output := h.Environments()
	return DblinkDeleteSQL(h.Operation().Context(), target, schema, qualified, values[1], values[2], values[3], output)
}

// DblinkDeleteSQL consumes detached immutable metadata and typed flat payloads.
// It is deliberately independent of storage, source tuples and statement hosts.
func DblinkDeleteSQL(ctx context.Context, target *catalog.Relation, schema string, qualified bool, keys, count, values sqltype.Value, output sqltype.OutputEnvironment) (string, error) {
	requested, err := count.Integer()
	if err != nil { return "", err }
	effective := min(requested, int64(keys.ArrayLen()))
	if effective <= 0 { return "", sqlError("22023", "number of key attributes must be > 0") }
	// Validate the used logical prefix before target cardinality or allocation.
	for i := range int(effective) {
		if err := ctx.Err(); err != nil { return "", err }
		number, err := keys.ArrayElement(i).Integer()
		if err != nil { return "", err }
		if number <= 0 || number > int64(len(target.Columns)) { return "", sqlError("22023", fmt.Sprintf("invalid attribute number %d", number)) }
	}
	if values.ArrayLen() != int(effective) { return "", sqlError("2202E", "target key array length must match number of key attributes") }
	identifier := catalog.DependencyIdentifier
	if output.QuoteAllIdentifiers { identifier = catalog.QuoteIdentifier }
	var out strings.Builder
	out.WriteString("DELETE FROM ")
	if qualified { out.WriteString(identifier(schema)); out.WriteByte('.') }
	out.WriteString(identifier(target.Name()))
	out.WriteString(" WHERE ")
	for i := range int(effective) {
		if err := ctx.Err(); err != nil { return "", err }
		if i != 0 { out.WriteString(" AND ") }
		number, _ := keys.ArrayElement(i).Integer() // used prefix validated above
		out.WriteString(identifier(target.Columns[number-1].Name))
		value := values.ArrayElement(i)
		if value.IsNull() {
			out.WriteString(" IS NULL")
		} else {
			out.WriteString(" = ")
			if err := functions.AppendSQLLiteral(ctx, &out, value, output); err != nil { return "", err }
		}
	}
	if err := ctx.Err(); err != nil { return "", err }
	return out.String(), nil
}
