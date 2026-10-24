//go:build !noservertests

package reference_test

import (
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"stuntgres/reference/fixture"
	"testing"
)

func definitionWireAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *pgconn.PgConn) {
	t.Helper()
	definitionWireSimple(t, p.Ctx, c, `CREATE ROLE tx_wire_reader; GRANT USAGE,CREATE ON SCHEMA public TO tx_wire_reader`, []string{"CREATE ROLE", "GRANT"}, 'I', nil)
	defer func() { c.Exec(p.Ctx, `RESET ROLE; DROP OWNED BY tx_wire_reader; DROP ROLE tx_wire_reader`).ReadAll() }()
	q := `CREATE ACCESS METHOD tx_wire_denied TYPE INDEX HANDLER bthandler`
	definitionWirePrepare(t, c, "actor_held", q, 'I')
	definitionWireScalar(t, p.Ctx, peer, `SELECT count(*) FROM pg_am WHERE amname='tx_wire_denied'`, "0")
	definitionWireSimple(t, p.Ctx, c, `BEGIN; SAVEPOINT actor; SET ROLE tx_wire_reader`, []string{"BEGIN", "SAVEPOINT", "SET"}, 'T', nil)
	definitionWireBind(t, c, "actor_held")
	definitionWireFlush(t, c, &pgproto3.Execute{Portal: "actor_held"}, &pgproto3.Sync{})
	response, err := c.Frontend().Receive()
	if err != nil {
		t.Fatal(err)
	}
	failure, ok := response.(*pgproto3.ErrorResponse)
	if !ok {
		t.Fatalf("held Execute got %#v want error", response)
	}
	want := fixture.ControlsReferenceDiagnostic("42501", `permission denied to create access method "tx_wire_denied"`)
	want.Hint = `Must be superuser to create an access method.`
	fixture.AssertDiagnostic(t, pgconn.ErrorResponseToPgError(failure), &want)
	definitionWireReady(t, c, 'E')
	// Sync has consumed the failed extended cycle, not recovered the transaction.
	definitionWireFlush(t, c, &pgproto3.Sync{})
	definitionWireReady(t, c, 'E')
	aborted := definitionAborted()
	_, err = c.Exec(p.Ctx, `SELECT 1`).ReadAll()
	fixture.AssertDiagnostic(t, err, &aborted)
	definitionWireSimple(t, p.Ctx, c, `ROLLBACK TO actor`, []string{"ROLLBACK"}, 'T', nil)
	definitionWireBind(t, c, "actor_held")
	definitionWireFlush(t, c, &pgproto3.Execute{Portal: "actor_held"}, &pgproto3.Sync{})
	command, ok := definitionWireReceive(t, c).(*pgproto3.CommandComplete)
	if !ok || string(command.CommandTag) != "CREATE ACCESS METHOD" {
		t.Fatalf("healthy held reuse %#v", command)
	}
	definitionWireReady(t, c, 'T')
	definitionWireScalar(t, p.Ctx, c, `SELECT count(*) FROM pg_am WHERE amname='tx_wire_denied' AND amhandler=330 AND amtype='i'`, "1")
	definitionWireScalar(t, p.Ctx, peer, `SELECT count(*) FROM pg_am WHERE amname='tx_wire_denied'`, "0")
	definitionWireSimple(t, p.Ctx, c, `ROLLBACK`, []string{"ROLLBACK"}, 'I', nil)
	definitionWireScalar(t, p.Ctx, peer, `SELECT count(*) FROM pg_am WHERE amname='tx_wire_denied'`, "0")
}

func definitionServerGoTransactions(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	run := definitionRun(t, p, b, c)
	run(fixture.Exec(`CREATE SCHEMA tx_wire_dest`, 0))
	run(fixture.Exec(`CREATE ROLE tx_wire_owner`, 0))
	run(fixture.Exec(`GRANT CREATE ON SCHEMA tx_wire_dest TO tx_wire_owner`, 0))
	defer func() {
		c.ExecContext(p.Ctx, `ROLLBACK`)
		c.ExecContext(p.Ctx, `DROP SCHEMA tx_wire_dest CASCADE`)
		c.ExecContext(p.Ctx, `DROP OWNED BY tx_wire_owner`)
		c.ExecContext(p.Ctx, `DROP ROLE tx_wire_owner`)
	}()
	for _, held := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			r := definitionRoute{mode: "go", held: held, commit: commit}
			queries := []string{`CREATE ACCESS METHOD tx_wire_go_am TYPE INDEX HANDLER bthandler`, `CREATE OPERATOR FAMILY public.tx_wire_go_family USING tx_wire_go_am`, `CREATE OPERATOR CLASS public.tx_wire_go_class DEFAULT FOR TYPE integer USING tx_wire_go_am FAMILY public.tx_wire_go_family AS ` + definitionClassItems, `ALTER OPERATOR FAMILY public.tx_wire_go_family USING tx_wire_go_am ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`, `ALTER OPERATOR FAMILY public.tx_wire_go_family USING tx_wire_go_am DROP FUNCTION 2(integer,integer)`, `ALTER OPERATOR FAMILY public.tx_wire_go_family USING tx_wire_go_am SET SCHEMA tx_wire_dest`, `ALTER OPERATOR FAMILY tx_wire_dest.tx_wire_go_family USING tx_wire_go_am OWNER TO tx_wire_owner`, `ALTER OPERATOR CLASS public.tx_wire_go_class USING tx_wire_go_am SET SCHEMA tx_wire_dest`, `ALTER OPERATOR CLASS tx_wire_dest.tx_wire_go_class USING tx_wire_go_am OWNER TO tx_wire_owner`}
			command := r.commands(t, p, b, c, queries)
			e, end := r.begin(t, p, b, c)
			for _, q := range queries {
				command(e, q)
			}
			definitionRun(t, p, b, e)(fixture.Select(`SELECT n.nspname,r.rolname,c.opcdefault,c.opcintype::bigint,c.opcmethod=a.oid,c.opcfamily=f.oid FROM pg_opclass c JOIN pg_namespace n ON n.oid=c.opcnamespace JOIN pg_roles r ON r.oid=c.opcowner JOIN pg_am a ON a.amname='tx_wire_go_am' JOIN pg_opfamily f ON f.opfname='tx_wire_go_family' WHERE c.opcname='tx_wire_go_class'`, []string{"nspname", "rolname", "opcdefault", "opcintype", "?column?", "?column?"}, []string{"NAME", "NAME", "BOOL", "INT8", "BOOL", "BOOL"}, [][]any{{"tx_wire_dest", "tx_wire_owner", true, int64(23), true, true}}))
			definitionRun(t, p, b, b.DB)(definitionCount(`SELECT count(*) FROM pg_am WHERE amname='tx_wire_go_am'`, 0))
			end()
			n := int64(0)
			if commit {
				n = 1
			}
			run(definitionCount(`SELECT count(*) FROM pg_opclass WHERE opcname='tx_wire_go_class'`, n))
			if commit {
				run(fixture.Exec(`DROP OPERATOR CLASS tx_wire_dest.tx_wire_go_class USING tx_wire_go_am`, 0))
				run(fixture.Exec(`DROP OPERATOR FAMILY tx_wire_dest.tx_wire_go_family USING tx_wire_go_am`, 0))
				run(fixture.Exec(`DROP ACCESS METHOD tx_wire_go_am`, 0))
			}
		}
	}
}
