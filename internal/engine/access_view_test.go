package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// fixedAccessEnvironment answers the two session inputs of the access checks
// with fixed values, independent of any session.
type fixedAccessEnvironment struct {
	temporary ids.ObjectAddress
	path      []ids.ObjectAddress
}

func (f fixedAccessEnvironment) TemporaryNamespace() ids.ObjectAddress { return f.temporary }
func (f fixedAccessEnvironment) ReferencePath() ([]ids.ObjectAddress, error) {
	return f.path, nil
}

func accessViewNamespace(t *testing.T, view catalog.Read, name string) ids.ObjectAddress {
	t.Helper()
	addresses := view.Lookup(ids.NameKey{ClassID: ids.NamespaceClassID, Name: name})
	if len(addresses) != 1 {
		t.Fatalf("namespace %s: %v", name, addresses)
	}
	return addresses[0]
}

func TestAccessViewRejectsForeignOwner(t *testing.T) {
	e, foreign := New(), New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	auth, err := op.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	public := auth.Ref(accessViewNamespace(t, auth.Catalog, "public"))
	if err := auth.CanUse(public); err != nil {
		t.Fatalf("own owner: %v", err)
	}
	stolen := auth
	stolen.Owner = foreign.catalog.owner()
	if err := stolen.CanUse(public); err == nil || !strings.Contains(err.Error(), "foreign or stale") {
		t.Fatalf("foreign owner: %v", err)
	}
	stolen.Owner = nil
	if _, err := stolen.Effective(public, false); err == nil || !strings.Contains(err.Error(), "foreign or stale") {
		t.Fatalf("missing owner: %v", err)
	}
}

func TestAccessViewTemporaryNamespaceFromEnvironment(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER temp_reader")
	roleExecute(t, e, op, "CREATE TEMP TABLE scratch(a INT)")
	var temporary ids.ObjectAddress
	for address, d := range e.CatalogRead().Descriptors() {
		if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary != (ids.SessionToken{}) && !ns.TemporaryToast {
			temporary = address
		}
	}
	if temporary == (ids.ObjectAddress{}) {
		t.Fatal("missing temporary namespace")
	}
	readerOp, readerDone := roleOperation(t, e, "temp_reader")
	defer readerDone()
	effective := func() (none, own authorization.Privileges) {
		t.Helper()
		auth, err := readerOp.Authorization()
		if err != nil {
			t.Fatal(err)
		}
		ref := auth.Ref(temporary)
		if none, err = auth.View.Effective(fixedAccessEnvironment{}, ref, false); err != nil {
			t.Fatal(err)
		}
		if own, err = auth.View.Effective(fixedAccessEnvironment{temporary: temporary}, ref, false); err != nil {
			t.Fatal(err)
		}
		return none, own
	}
	none, own := effective()
	if none != 0 || own != authorization.UsagePrivilege|authorization.CreatePrivilege {
		t.Fatalf("with database TEMP: none=%v own=%v", none, own)
	}
	view := e.CatalogRead()
	owner := view.MustDatabase(catalog.CurrentDatabaseAddress).Owner
	ownerOnly := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: owner}, Privileges: authorization.DatabasePrivileges}}}
	if err := e.SetObjectACL(context.Background(), e.catalog.Domain.Ref(view.Epoch(), catalog.CurrentDatabaseAddress), ownerOnly); err != nil {
		t.Fatal(err)
	}
	none, own = effective()
	if none != 0 || own != authorization.UsagePrivilege {
		t.Fatalf("without database TEMP: none=%v own=%v", none, own)
	}
}

func TestAccessViewTypeDenialQualifiesByReferencePath(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER type_reader")
	roleExecute(t, e, op, "CREATE SCHEMA moods")
	roleExecute(t, e, op, "GRANT USAGE ON SCHEMA moods TO type_reader")
	roleExecute(t, e, op, "CREATE TYPE moods.mood AS ENUM ('ok')")
	roleExecute(t, e, op, "REVOKE USAGE ON TYPE moods.mood FROM PUBLIC")
	readerOp, readerDone := roleOperation(t, e, "type_reader")
	defer readerDone()
	auth, err := readerOp.Authorization()
	if err != nil {
		t.Fatal(err)
	}
	schema := accessViewNamespace(t, auth.Catalog, "moods")
	public := accessViewNamespace(t, auth.Catalog, "public")
	address, ok := auth.Catalog.LookupName(ids.NameKey{ClassID: ids.TypeClassID, Namespace: schema, Name: "mood"})
	if !ok {
		t.Fatal("missing type")
	}
	req := access.Requirement{Object: auth.Ref(address), Privileges: authorization.UsagePrivilege}
	for _, tc := range []struct {
		path []ids.ObjectAddress
		want string
	}{
		{[]ids.ObjectAddress{public}, "permission denied for type moods.mood"},
		{[]ids.ObjectAddress{schema}, "permission denied for type mood"},
	} {
		err := auth.View.Check(fixedAccessEnvironment{path: tc.path}, req)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42501" || diagnostic.Message != tc.want {
			t.Fatalf("path %v: got %v, want %q", tc.path, err, tc.want)
		}
	}
}
