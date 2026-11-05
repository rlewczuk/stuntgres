package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func sslInfoCertificateIdentities(t *testing.T, p *fixture.Pair, c *sql.Conn) []int64 {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT p.oid::bigint FROM pg_proc p JOIN pg_depend d ON d.classid=1255 AND d.objid=p.oid AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='sslinfo' ORDER BY p.oid")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil || id <= 0 {
			t.Fatal("member identity", id, err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(ids) != 10 {
		t.Fatalf("attached identities=%v, want ten", ids)
	}
	return ids
}

func TestReferenceSSLInfoCertificateIdentity(t *testing.T) {
	sslInfoCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		sslInfoInstall(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// Literal released declarations, independent of the native member table.
		q := "SELECT p.proname,p.pronargs,p.proargtypes::text,p.prorettype::bigint AS result_oid,p.proretset,p.proallargtypes::text,p.proargmodes::text,p.proargnames::text,p.proisstrict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prokind::text AS kind,l.lanname,p.prosrc,p.probin,p.prosecdef,p.procost::bigint AS cost,p.prorows::bigint AS rows FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='ssl_part' AND p.proname IN ('ssl_client_dn_field','ssl_issuer_field','ssl_extension_info') ORDER BY p.proname"
		want := [][]any{
			{"ssl_client_dn_field", int64(1), "25", int64(25), false, nil, nil, nil, true, "v", "r", "f", "c", "ssl_client_dn_field", "$libdir/sslinfo", false, int64(1), int64(0)},
			{"ssl_extension_info", int64(0), "", int64(2249), true, "{25,25,16}", "{o,o,o}", "{name,value,critical}", true, "v", "r", "f", "c", "ssl_extension_info", "$libdir/sslinfo", false, int64(1), int64(1000)},
			{"ssl_issuer_field", int64(1), "25", int64(25), false, nil, nil, nil, true, "v", "r", "f", "c", "ssl_issuer_field", "$libdir/sslinfo", false, int64(1), int64(0)},
		}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select(q, []string{"proname", "pronargs", "proargtypes", "result_oid", "proretset", "proallargtypes", "proargmodes", "proargnames", "proisstrict", "volatility", "parallel", "kind", "lanname", "prosrc", "probin", "prosecdef", "cost", "rows"}, []string{"NAME", "INT2", "TEXT", "INT8", "BOOL", "TEXT", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "NAME", "TEXT", "TEXT", "BOOL", "INT8", "INT8"}, want)
			s.Prepared = prepared
			run(s)
			members := fixture.Select("SELECT p.proname FROM pg_proc p JOIN pg_depend d ON d.classid=1255 AND d.objid=p.oid AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='sslinfo' ORDER BY p.proname", []string{"proname"}, []string{"NAME"}, [][]any{{"ssl_cipher"}, {"ssl_client_cert_present"}, {"ssl_client_dn"}, {"ssl_client_dn_field"}, {"ssl_client_serial"}, {"ssl_extension_info"}, {"ssl_is_used"}, {"ssl_issuer_dn"}, {"ssl_issuer_field"}, {"ssl_version"}})
			members.Prepared = prepared
			run(members)
		}
		sslInfoCertificateIdentities(t, p, c)
		sslInfoInventory(t, p, c, b.Recorder)
		run(sslInfoSelect("SELECT " + sslInfoProjection))
	})
}
