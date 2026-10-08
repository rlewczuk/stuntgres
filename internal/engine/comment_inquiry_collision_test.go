package engine

import (
	"context"
	"fmt"
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
	// Equal inputs must form one group, regardless of which candidate wins.
	// This is a relational invariant over genuine catalog/table state, not
	// an assertion that enough random scans eventually choose both candidates.
	roleExecute(t, e, op, "CREATE TABLE collision_input(a INT)")
	roleExecute(t, e, op, "INSERT INTO collision_input VALUES(1),(1),(2)")
	repeated := tuples(roleExecute(t, e, op, "SELECT obj_description(20000),obj_description(20000) FROM collision_input"))
	if len(repeated) != 3 {
		t.Fatal(repeated)
	}
	winner := repeated[0][0]
	if winner != "relation candidate" && winner != "namespace candidate" {
		t.Fatal("legacy candidate membership", winner)
	}
	for _, row := range repeated {
		if len(row) != 2 || row[0] != winner || row[1] != winner {
			t.Fatal("equal arguments changed within statement", repeated)
		}
	}
	grouped := tuples(roleExecute(t, e, op, "SELECT obj_description(20000),count(*) FROM collision_input GROUP BY obj_description(20000)"))
	if len(grouped) != 1 || len(grouped[0]) != 2 || grouped[0][1] != int64(3) || (grouped[0][0] != "relation candidate" && grouped[0][0] != "namespace candidate") {
		t.Fatal("equal inputs split into inconsistent groups", grouped)
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

func TestCommentInquiryRoleNameReuse(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE ROLE named_comment")
	first, ok := e.Roles().Lookup("named_comment")
	if !ok {
		t.Fatal("created role absent")
	}
	check := func(id uint64, want any) {
		t.Helper()
		got := tuples(roleExecute(t, e, op, fmt.Sprintf("SELECT shobj_description(%d,'pg_authid')", id)))
		if len(got) != 1 || len(got[0]) != 1 || got[0][0] != want {
			t.Fatal("actual principal inquiry", id, got, want)
		}
	}
	for _, tc := range []struct {
		sql  string
		want any
	}{
		{"'old identity'", "old identity"},
		{"'replacement'", "replacement"},
		{"NULL", nil},
		{"''", nil},
		{"' '", " "},
	} {
		roleExecute(t, e, op, "COMMENT ON ROLE named_comment IS "+tc.sql)
		check(first.Ref.ID(), tc.want)
	}
	roleExecute(t, e, op, "COMMENT ON ROLE named_comment IS 'old identity'")
	roleExecute(t, e, op, "DROP ROLE named_comment")
	roleExecute(t, e, op, "CREATE ROLE named_comment")
	second, ok := e.Roles().Lookup("named_comment")
	if !ok || second.Ref == first.Ref {
		t.Fatal("recreated role did not obtain fresh identity", first, second)
	}
	check(first.Ref.ID(), nil)
	check(second.Ref.ID(), nil)
	roleExecute(t, e, op, "COMMENT ON ROLE named_comment IS 'new identity'")
	check(second.Ref.ID(), "new identity")
	check(first.Ref.ID(), nil)
}
