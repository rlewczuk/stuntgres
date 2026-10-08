//go:build reference

package reference_test

import "testing"

func TestReferenceCommentInquiryLegacyCollision(t *testing.T) {
	t.Run("Oracle", func(t *testing.T) {
		p := newReferencePair(t)
		// Genuine description storage with a private high OID, scoped by a
		// transaction so every injected row is removed even on an assertion failure.
		tx, err := p.postgres.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if _, err = tx.ExecContext(p.ctx, "INSERT INTO pg_description(objoid,classoid,objsubid,description) VALUES(4294967294,1259,0,'relation candidate'),(4294967294,2615,0,'namespace candidate'),(4294967294,1259,1,'column excluded')"); err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct{ q, want string }{{"SELECT obj_description(4294967294::oid,'pg_class')", "relation candidate"}, {"SELECT obj_description(4294967294::oid,'pg_namespace')", "namespace candidate"}, {"SELECT col_description(4294967294::oid,1)", "column excluded"}} {
			referenceRunStep(t, p.ctx, tx, p.postgresRecord, referenceSelect(tc.q, []string{map[bool]string{true: "col_description", false: "obj_description"}[tc.want == "column excluded"]}, []string{"TEXT"}, [][]any{{tc.want}}))
		}
		var got string
		if err := tx.QueryRowContext(p.ctx, "SELECT obj_description(4294967294::oid)").Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != "relation candidate" && got != "namespace candidate" {
			t.Fatal("legacy candidate membership", got)
		}
	})
}
