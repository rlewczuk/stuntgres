package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func TestCommentInquiryLegacyCollision(t *testing.T) {
	e := engine.New()
	base := e.CatalogRead()
	stage := e.BeginCatalogStage(base)
	owner := e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	if err := stage.Create(&catalog.Namespace{ObjectAddress: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 20000}, Owner: owner, ObjectName: "collision", ObjectRevision: 1, Comment: "namespace candidate"}); err != nil {
		t.Fatal(err)
	}
	if err := stage.Create(&catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000}, Owner: owner, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectName: "collision", ObjectRevision: 1, Comment: "relation candidate", Columns: []catalog.Column{{Name: "a", Usage: enginetest.Usage(e, sqltype.Int4)}}, Index: map[string]int{"a": 0}, Attributes: []int32{1}, ColumnMeta: catalog.ColumnMetadata{HighWater: 1, Comments: map[int32]string{1: "column excluded"}}}); err != nil {
		t.Fatal(err)
	}
	p, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, p, engine.PublicationToken{Epoch: base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	e.RoleStore.InstallPrincipalFrontierLocked(20000)
	enginetest.RoleExecute(t, e, op, "CREATE ROLE shared_collision")
	enginetest.RoleExecute(t, e, op, "COMMENT ON ROLE shared_collision IS 'shared excluded'")
	shared := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT shobj_description(20000,'pg_authid')"))
	if len(shared) != 1 || shared[0][0] != "shared excluded" {
		t.Fatal(shared)
	}
	for _, tc := range []struct{ q, want string }{{"SELECT obj_description(20000,'pg_class')", "relation candidate"}, {"SELECT obj_description(20000,'pg_namespace')", "namespace candidate"}, {"SELECT col_description(20000,1)", "column excluded"}} {
		got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, tc.q))
		if len(got) != 1 || got[0][0] != tc.want {
			t.Fatal(tc.q, got)
		}
	}
	got := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT obj_description(20000)"))
	if len(got) != 1 || (got[0][0] != "relation candidate" && got[0][0] != "namespace candidate") {
		t.Fatal("legacy candidate membership", got)
	}
	// Equal inputs must form one group, regardless of which candidate wins.
	// This is a relational invariant over genuine catalog/table state, not
	// an assertion that enough random scans eventually choose both candidates.
	enginetest.RoleExecute(t, e, op, "CREATE TABLE collision_input(a INT)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO collision_input VALUES(1),(1),(2)")
	repeated := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT obj_description(20000),obj_description(20000) FROM collision_input"))
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
	grouped := enginetest.Tuples(enginetest.RoleExecute(t, e, op, "SELECT obj_description(20000),count(*) FROM collision_input GROUP BY obj_description(20000)"))
	if len(grouped) != 1 || len(grouped[0]) != 2 || grouped[0][1] != int64(3) || (grouped[0][0] != "relation candidate" && grouped[0][0] != "namespace candidate") {
		t.Fatal("equal inputs split into inconsistent groups", grouped)
	}
}
