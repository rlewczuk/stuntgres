package engine

import (
	"context"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestCommentInquiryLegacyCollision(t *testing.T) {
	e := New()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	owner := e.roles.names["postgres"]
	if err := stage.Create(&namespaceDescriptor{address: ObjectAddress{ClassID: NamespaceClassID, ObjectID: 20000}, owner: owner, name: "collision", revision: 1, comment: "namespace candidate"}); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(&relationDescriptor{address: ObjectAddress{ClassID: RelationClassID, ObjectID: 20000}, owner: owner, namespace: ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}, name: "collision", revision: 1, comment: "relation candidate", columns: []ColumnDescriptor{{Name: "a", Usage: testUsage(e, sqltype.Int4)}}, index: map[string]int{"a": 0}, attributes: []int32{1}, columnComments: map[int32]string{1: "column excluded"}}); err != nil {
		t.Fatal(err)
	}
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(p, PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.nextPrincipal = 20000
	roleExecute(t, e, op, "CREATE ROLE shared_collision")
	roleExecute(t, e, op, "COMMENT ON ROLE shared_collision IS 'shared excluded'")
	shared := tuples(roleExecute(t, e, op, "SELECT shobj_description(20000,'pg_authid')"))
	if len(shared) != 1 || shared[0][0] != "shared excluded" {
		t.Fatal(shared)
	}
	for _, tc := range []struct{ q, want string }{{"SELECT obj_description(20000,'pg_class')", "relation candidate"}, {"SELECT obj_description(20000,'pg_namespace')", "namespace candidate"}, {"SELECT col_description(20000,1)", "column excluded"}} {
		got := tuples(roleExecute(t, e, op, tc.q))
		if len(got) != 1 || got[0][0] != tc.want {
			t.Fatal(tc.q, got)
		}
	}
	got := tuples(roleExecute(t, e, op, "SELECT obj_description(20000)"))
	if len(got) != 1 || (got[0][0] != "relation candidate" && got[0][0] != "namespace candidate") {
		t.Fatal("legacy candidate membership", got)
	}
}

func TestCommentInquirySharedIdentityWidth(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	e.nextPrincipal = 20000
	roleExecute(t, e, op, "CREATE ROLE low")
	roleExecute(t, e, op, "COMMENT ON ROLE low IS 'low shared'")
	e.nextPrincipal = uint64(1)<<32 | 20000
	roleExecute(t, e, op, "CREATE ROLE high")
	roleExecute(t, e, op, "COMMENT ON ROLE high IS 'high excluded'")
	got := tuples(roleExecute(t, e, op, "SELECT shobj_description(20000,'pg_authid')"))
	if len(got) != 1 || got[0][0] != "low shared" {
		t.Fatal("truncated principal match", got)
	}
	roleExecute(t, e, op, "DROP ROLE low")
	got = tuples(roleExecute(t, e, op, "SELECT shobj_description(20000,'pg_authid')"))
	if len(got) != 1 || got[0][0] != nil {
		t.Fatal("high principal aliased missing low", got)
	}
}
