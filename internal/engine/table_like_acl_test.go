package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestTableLikeSourceACL(t *testing.T) {
	e := New()
	run(t, e, "CREATE USER like_reader")
	run(t, e, "CREATE TABLE src(a INT NOT NULL DEFAULT 7)")
	run(t, e, "CREATE SEQUENCE seq")
	run(t, e, "CREATE INDEX idx ON src(a)")
	run(t, e, "CREATE SCHEMA private_source")
	run(t, e, "CREATE TABLE private_source.src(a INT)")
	source := roleTableRef(t, e, "src")
	reader, _ := e.Roles().Lookup("like_reader")
	owner := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	op, done := roleOperation(t, e, "like_reader")
	defer done()
	for _, tc := range []struct {
		query string
		want  sqlstate.Error
	}{
		{"CREATE TABLE clone (LIKE src INCLUDING ALL)", sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table src"}},
		{"CREATE TABLE clone (LIKE seq INCLUDING ALL)", sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `relation "seq" is invalid in LIKE clause`, Detail: "This operation is not supported for sequences.", Position: "26"}},
		{"CREATE TABLE clone (LIKE idx INCLUDING ALL)", sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: `relation "idx" is invalid in LIKE clause`, Detail: "This operation is not supported for indexes.", Position: "26"}},
		{"CREATE TABLE clone (LIKE private_source.src INCLUDING ALL)", sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for schema private_source", Position: "26"}},
	} {
		if _, err := e.Describe(op, statement(t, tc.query)); err != nil {
			t.Fatal("semantic utility failed at Prepare", err)
		}
		stage, _, err := tableLikeBuild(t, e, op, tc.query)
		e.DiscardCatalogStage(stage)
		var got *sqlstate.Error
		if !errors.As(err, &got) || !reflect.DeepEqual(*got, tc.want) {
			t.Fatal(err, got, tc.want)
		}
	}
	grant := authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: owner, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: authorization.SelectPrivilege}}}
	column := source
	column.Address.SubID = 1
	if err := e.SetObjectACL(context.Background(), column, grant); err != nil {
		t.Fatal(err)
	}
	stage, _, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(LIKE src INCLUDING ALL)")
	e.DiscardCatalogStage(stage)
	var denied *sqlstate.Error
	if !errors.As(err, &denied) || denied.Code != "42501" || denied.Message != "permission denied for table src" || denied.Position != "" {
		t.Fatal("column grant admitted LIKE", err)
	}
	if err := e.SetObjectACL(context.Background(), source, grant); err != nil {
		t.Fatal(err)
	}
	stage, destination, err := tableLikeBuild(t, e, op, "CREATE TABLE clone(LIKE src INCLUDING ALL)")
	if err != nil {
		t.Fatal(err)
	}
	defer e.DiscardCatalogStage(stage)
	if destination.owner != reader.Ref || destination.acl.Explicit || len(destination.columnACL) != 0 || len(destination.defaults) != 1 {
		t.Fatal("source authority copied", destination)
	}
	captured := false
	for _, descriptor := range stage.likeSources {
		captured = captured || descriptor == e.CatalogRead().Get(source.Address)
	}
	if !captured {
		t.Fatal("source admission capture absent")
	}
}
