package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatSSLShape(t *testing.T) {
	activityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		a, other := statActivityConn(t, p, b), statActivityConn(t, p, b)
		apid, bpid := statActivityPID(t, p, a), statActivityPID(t, p, other)
		for _, query := range []string{sslPairSQL, "SELECT * FROM pg_stat_ssl WHERE pid IN ($1,$2) ORDER BY pid", "SELECT * FROM pg_catalog.pg_stat_ssl WHERE FALSE"} {
			held := sslHeld(t, p, c, query)
			defer held.Statement.Close()
			for _, executor := range []fixture.Executor{c, held, held} {
				step := sslStep(query, b.PG, nil)
				if query != "SELECT * FROM pg_catalog.pg_stat_ssl WHERE FALSE" {
					step = sslStep(query, b.PG, []int64{apid, bpid}, apid, bpid)
				}
				fixture.RunStep(t, p.Ctx, executor, b.Recorder, step)
			}
		}
		held := sslHeld(t, p, c, sslSQL)
		defer held.Statement.Close()
		for _, executor := range []fixture.Executor{c, held, held} {
			sslWant(t, p, b, executor, apid, true)
		}
		var attributes [][]any
		for i, name := range sslNames {
			oid, collation := int64(25), int64(100)
			switch i {
			case 0, 4:
				oid, collation = 23, 0
			case 1:
				oid, collation = 16, 0
			case 6:
				oid, collation = 1700, 0
			}
			attributes = append(attributes, []any{int64(i + 1), name, oid, false, int64(-1), collation, int64(0)})
		}
		shape := fixture.Select("SELECT attnum,attname,atttypid,attnotnull,atttypmod,attcollation,attndims FROM pg_attribute WHERE attrelid='pg_catalog.pg_stat_ssl'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attnum", "attname", "atttypid", "attnotnull", "atttypmod", "attcollation", "attndims"}, []string{"INT2", "NAME", "OID", "BOOL", "INT4", "OID", "INT2"}, attributes)
		links := fixture.Select("SELECT c.relkind,c.relnatts,c.oid=t.typrelid AS relation_link,t.typarray=a.oid AS array_link,a.typelem=t.oid AS element_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid='pg_catalog.pg_stat_ssl'::regclass", []string{"relkind", "relnatts", "relation_link", "array_link", "element_link"}, []string{"CHAR", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"v", int64(8), true, true, true}})
		for _, step := range []fixture.Step{shape, links} {
			fixture.PgDescriptionExecutorRows(t, p, b, c, step)
			fixture.PgDescriptionExecutorRows(t, p, b, c, fixture.Prepared(step))
		}
		if !b.PG {
			identity := fixture.Select("SELECT c.oid,c.reltype,t.typarray FROM pg_class c JOIN pg_type t ON t.oid=c.reltype WHERE c.oid='pg_catalog.pg_stat_ssl'::regclass", []string{"oid", "reltype", "typarray"}, []string{"OID", "OID", "OID"}, [][]any{{int64(12253), int64(12255), int64(12254)}})
			fixture.PgDescriptionExecutorRows(t, p, b, c, identity)
		}
		const definition = ` SELECT pid,
    ssl,
    sslversion AS version,
    sslcipher AS cipher,
    sslbits AS bits,
    ssl_client_dn AS client_dn,
    ssl_client_serial AS client_serial,
    ssl_issuer_dn AS issuer_dn
   FROM pg_stat_get_activity(NULL::integer) s(datid, pid, usesysid, application_name, state, query, wait_event_type, wait_event, xact_start, query_start, backend_start, state_change, client_addr, client_hostname, client_port, backend_xid, backend_xmin, backend_type, ssl, sslversion, sslcipher, sslbits, ssl_client_dn, ssl_client_serial, ssl_issuer_dn, gss_auth, gss_princ, gss_enc, gss_delegation, leader_pid, query_id)
  WHERE (client_port IS NOT NULL);`
		for _, query := range []string{"SELECT definition FROM pg_views WHERE schemaname='pg_catalog' AND viewname='pg_stat_ssl'", "SELECT pg_get_viewdef('pg_catalog.pg_stat_ssl'::regclass) AS definition"} {
			step := fixture.Select(query, []string{"definition"}, []string{"TEXT"}, [][]any{{definition}})
			fixture.RunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(step))
		}
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, executor := range []fixture.Executor{c, held} {
				rows, err := executor.QueryContext(ctx, sslSQL, apid)
				if rows != nil {
					rows.Close()
					t.Fatal("pre-canceled SSL returned rows")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatal("ordinary SSL cancellation", err)
				}
			}
		}
	})
}
