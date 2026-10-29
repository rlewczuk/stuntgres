package reference_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func ddlCommandsTemporaryNamespaceIdentity(t *testing.T) {
	ddlCommandScenario(t, func(owner ddlCommandCase) {
		peerConn, err := owner.b.DB.Conn(owner.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peerConn.Close()
		peer := owner
		peer.c = peerConn
		owner.exec("CREATE TEMP TABLE et_dc_foreign_temp(i integer)")
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := owner.c.ExecContext(ctx, "DROP TABLE IF EXISTS pg_temp.et_dc_foreign_temp"); err != nil {
				t.Error(err)
			}
		}()
		var oid, namespace, quotedNamespace string
		if err := owner.c.QueryRowContext(owner.p.Ctx, "SELECT c.oid::text,n.nspname,quote_ident(n.nspname) FROM pg_class c JOIN pg_namespace n ON c.relnamespace=n.oid WHERE c.oid='pg_temp.et_dc_foreign_temp'::regclass").Scan(&oid, &namespace, &quotedNamespace); err != nil {
			t.Fatal(err)
		}
		if oid == "0" || !strings.HasPrefix(namespace, "pg_temp_") {
			t.Fatalf("temporary catalog address: oid=%q namespace=%q", oid, namespace)
		}
		owner.install("'COMMENT'")
		for _, prepared := range []bool{false, true} {
			owner.clear()
			s := fixture.Exec("COMMENT ON TABLE pg_temp.et_dc_foreign_temp IS 'owner'", 0)
			s.Prepared = prepared
			owner.run(s)
			owner.rows(ddlCommandRows("1259", "COMMENT", "table", "pg_temp", "pg_temp.et_dc_foreign_temp"))
			address := fixture.Select("SELECT objid::text AS oid FROM et_dc_audit ORDER BY pass", []string{"oid"}, []string{"TEXT"}, [][]any{{oid}, {oid}})
			owner.run(address)
			owner.run(fixture.Prepared(address))
			owner.clear()
			// COMMENT is metadata-only: the peer never reads another session's temporary heap.
			s = fixture.Exec("COMMENT ON TABLE "+quotedNamespace+".et_dc_foreign_temp IS 'peer'", 0)
			s.Prepared = prepared
			peer.run(s)
			peer.rows(ddlCommandRows("1259", "COMMENT", "table", namespace, quotedNamespace+".et_dc_foreign_temp"))
			peer.run(address)
			peer.run(fixture.Prepared(address))
			relationship := fixture.Select("SELECT c.relpersistence,n.nspname FROM et_dc_audit a JOIN pg_class c ON a.objid=c.oid JOIN pg_namespace n ON c.relnamespace=n.oid WHERE a.classid='pg_class'::regclass ORDER BY a.pass", []string{"relpersistence", "nspname"}, []string{"CHAR", "NAME"}, [][]any{{"t", namespace}, {"t", namespace}})
			peer.run(relationship)
			peer.run(fixture.Prepared(relationship))
		}
	})
}
