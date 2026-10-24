//go:build !noservertests

package reference_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"
	"stuntgres/reference/fixture"
)

func definitionWireConnect(t *testing.T, ctx context.Context, dsn string) *pgconn.PgConn {
	t.Helper()
	c, err := pgconn.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	if deadline, ok := ctx.Deadline(); ok {
		if err := c.Conn().SetDeadline(deadline); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { c.Close(context.Background()) })
	return c
}

func definitionWireSimple(t *testing.T, ctx context.Context, c *pgconn.PgConn, q string, tags []string, status byte, want *fixture.Diagnostic) {
	t.Helper()
	results, err := c.Exec(ctx, q).ReadAll()
	if want != nil {
		fixture.AssertDiagnostic(t, err, want)
	} else if err != nil {
		t.Fatal(q, err)
	}
	var got []string
	for _, r := range results {
		got = append(got, r.CommandTag.String())
	}
	if !reflect.DeepEqual(got, tags) {
		t.Fatalf("%s command tags %q want %q", q, got, tags)
	}
	if c.TxStatus() != status {
		t.Fatalf("%s ReadyForQuery %c want %c", q, c.TxStatus(), status)
	}
}

func definitionWireReceive(t *testing.T, c *pgconn.PgConn) pgproto3.BackendMessage {
	t.Helper()
	for {
		// Receive through pgconn so asynchronous ParameterStatus updates its
		// public connection state, just as the normal query receiver does.
		// The connection retains the scenario's socket deadline.
		m, err := c.ReceiveMessage(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := m.(*pgproto3.ParameterStatus); ok {
			continue
		}
		if e, ok := m.(*pgproto3.ErrorResponse); ok {
			t.Fatalf("unexpected protocol error: %+v", e)
		}
		if n, ok := m.(*pgproto3.NoticeResponse); ok {
			t.Fatalf("unexpected notice during quiet utility: %+v", n)
		}
		return m
	}
}

func definitionWireFlush(t *testing.T, c *pgconn.PgConn, messages ...pgproto3.FrontendMessage) {
	t.Helper()
	for _, m := range messages {
		c.Frontend().Send(m)
	}
	if err := c.Frontend().Flush(); err != nil {
		t.Fatal(err)
	}
}

func definitionWireReady(t *testing.T, c *pgconn.PgConn, status byte) {
	t.Helper()
	m := definitionWireReceive(t, c)
	r, ok := m.(*pgproto3.ReadyForQuery)
	if !ok || r.TxStatus != status {
		t.Fatalf("ReadyForQuery got %#v want %c", m, status)
	}
}

func definitionWireUtility(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *pgconn.PgConn, name, q, tag, absence string, status byte, before ...string) {
	t.Helper()
	definitionWirePrepare(t, c, name, q, status)
	expected := "0"
	if len(before) != 0 {
		expected = before[0]
	}
	definitionWireScalar(t, p.Ctx, peer, absence, expected)
	// Flush, not Sync: the autocommit portal must remain alive for Execute.
	definitionWireBind(t, c, name)
	definitionWireScalar(t, p.Ctx, peer, absence, expected)
	definitionWireFlush(t, c, &pgproto3.Execute{Portal: name}, &pgproto3.Sync{})
	command, ok := definitionWireReceive(t, c).(*pgproto3.CommandComplete)
	if !ok || string(command.CommandTag) != tag {
		t.Fatalf("utility completion %#v want %s", command, tag)
	}
	definitionWireReady(t, c, status)
}

func definitionWireScalar(t *testing.T, ctx context.Context, c *pgconn.PgConn, q, want string) {
	t.Helper()
	results, err := c.Exec(ctx, q).ReadAll()
	if err != nil {
		t.Fatal(q, err)
	}
	if len(results) != 1 || len(results[0].Rows) != 1 || len(results[0].Rows[0]) != 1 || string(results[0].Rows[0][0]) != want {
		t.Fatalf("%s: %#v want %q", q, results, want)
	}
}

func definitionWirePrepare(t *testing.T, c *pgconn.PgConn, name, q string, status byte) {
	t.Helper()
	definitionWireFlush(t, c, &pgproto3.Parse{Name: name, Query: q}, &pgproto3.Describe{ObjectType: 'S', Name: name}, &pgproto3.Sync{})
	if _, ok := definitionWireReceive(t, c).(*pgproto3.ParseComplete); !ok {
		t.Fatal("missing ParseComplete")
	}
	params, ok := definitionWireReceive(t, c).(*pgproto3.ParameterDescription)
	if !ok || len(params.ParameterOIDs) != 0 {
		t.Fatalf("utility parameter signature %#v", params)
	}
	if _, ok := definitionWireReceive(t, c).(*pgproto3.NoData); !ok {
		t.Fatal("utility Describe(S) is not NoData")
	}
	definitionWireReady(t, c, status)
}

func definitionWireBind(t *testing.T, c *pgconn.PgConn, name string) {
	t.Helper()
	definitionWireFlush(t, c, &pgproto3.Bind{DestinationPortal: name, PreparedStatement: name}, &pgproto3.Describe{ObjectType: 'P', Name: name}, &pgproto3.Flush{})
	if _, ok := definitionWireReceive(t, c).(*pgproto3.BindComplete); !ok {
		t.Fatal("missing BindComplete")
	}
	if _, ok := definitionWireReceive(t, c).(*pgproto3.NoData); !ok {
		t.Fatal("utility Describe(P) is not NoData")
	}
}

func TestReferenceTransactionalDefinitionProtocol(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"tx_wire_reader", "tx_wire_owner"})
	fixture.SchemaReferenceCleanup(t, "tx_wire_dest")
	clean := fixture.AccessMethodReferenceCleanup(t, []string{"tx_wire_am", "tx_wire_failed", "tx_wire_go_am", "tx_wire_denied"})
	server := fixture.NewServerFixture(t)
	clean()
	for _, b := range server.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := server.DSN
			if b.PG {
				dsn = fixture.DSN
			}
			// Pair supplies the canonical bounded scenario context/diagnostic runner.
			p := fixture.NewScenario(t)
			c := definitionWireConnect(t, p.Ctx, dsn)
			peer := definitionWireConnect(t, p.Ctx, dsn)
			amAbsent := `SELECT count(*) FROM pg_am WHERE amname='tx_wire_am'`
			familyAbsent := `SELECT count(*) FROM pg_opfamily WHERE opfname='tx_wire_family'`
			classAbsent := `SELECT count(*) FROM pg_opclass WHERE opcname='tx_wire_class'`
			for _, explicit := range []bool{false, true} {
				for _, commit := range []bool{false, true} {
					if !explicit && !commit {
						continue
					}
					status := byte('I')
					if explicit {
						definitionWireSimple(t, p.Ctx, c, "BEGIN", []string{"BEGIN"}, 'T', nil)
						status = 'T'
					}
					prefix := fmt.Sprintf("utility_%t_%t_", explicit, commit)
					definitionWireUtility(t, p, b, c, peer, prefix+"am", `CREATE ACCESS METHOD tx_wire_am TYPE INDEX HANDLER bthandler`, "CREATE ACCESS METHOD", amAbsent, status)
					definitionWireUtility(t, p, b, c, peer, prefix+"family", `CREATE OPERATOR FAMILY public.tx_wire_family USING tx_wire_am`, "CREATE OPERATOR FAMILY", familyAbsent, status)
					definitionWireUtility(t, p, b, c, peer, prefix+"class", `CREATE OPERATOR CLASS public.tx_wire_class DEFAULT FOR TYPE integer USING tx_wire_am FAMILY public.tx_wire_family AS `+definitionClassItems, "CREATE OPERATOR CLASS", classAbsent, status)
					definitionWireScalar(t, p.Ctx, c, `SELECT count(*) FROM pg_opclass c JOIN pg_opfamily f ON f.oid=c.opcfamily JOIN pg_am a ON a.oid=c.opcmethod WHERE c.opcname='tx_wire_class' AND f.opfname='tx_wire_family' AND a.amname='tx_wire_am' AND c.opcdefault AND c.opcintype=23 AND a.amhandler=330`, "1")
					peerGraph := "0"
					if !explicit {
						peerGraph = "1"
					}
					memberQuery := `SELECT count(*) FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily WHERE f.opfname='tx_wire_family' AND a.amprocnum=2`
					definitionWireUtility(t, p, b, c, peer, prefix+"add", `ALTER OPERATOR FAMILY public.tx_wire_family USING tx_wire_am ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`, "ALTER OPERATOR FAMILY", memberQuery, status)
					memberBefore := "0"
					if !explicit {
						memberBefore = "1"
					}
					definitionWireUtility(t, p, b, c, peer, prefix+"remove", `ALTER OPERATOR FAMILY public.tx_wire_family USING tx_wire_am DROP FUNCTION 2(integer,integer)`, "ALTER OPERATOR FAMILY", memberQuery, status, memberBefore)
					definitionWireUtility(t, p, b, c, peer, prefix+"rename_class", `ALTER OPERATOR CLASS public.tx_wire_class USING tx_wire_am RENAME TO tx_wire_class_renamed`, "ALTER OPERATOR CLASS", classAbsent, status, peerGraph)
					definitionWireScalar(t, p.Ctx, c, `SELECT count(*) FROM pg_opclass WHERE opcname='tx_wire_class_renamed'`, "1")
					definitionWireUtility(t, p, b, c, peer, prefix+"restore_class", `ALTER OPERATOR CLASS public.tx_wire_class_renamed USING tx_wire_am RENAME TO tx_wire_class`, "ALTER OPERATOR CLASS", `SELECT count(*) FROM pg_opclass WHERE opcname='tx_wire_class_renamed'`, status, peerGraph)
					definitionWireUtility(t, p, b, c, peer, prefix+"rename_family", `ALTER OPERATOR FAMILY public.tx_wire_family USING tx_wire_am RENAME TO tx_wire_family_renamed`, "ALTER OPERATOR FAMILY", familyAbsent, status, peerGraph)
					definitionWireUtility(t, p, b, c, peer, prefix+"restore_family", `ALTER OPERATOR FAMILY public.tx_wire_family_renamed USING tx_wire_am RENAME TO tx_wire_family`, "ALTER OPERATOR FAMILY", `SELECT count(*) FROM pg_opfamily WHERE opfname='tx_wire_family_renamed'`, status, peerGraph)
					if explicit {
						definitionWireScalar(t, p.Ctx, peer, classAbsent, "0")
						end := "ROLLBACK"
						if commit {
							end = "COMMIT"
						}
						definitionWireSimple(t, p.Ctx, c, end, []string{end}, 'I', nil)
					}
					n := "0"
					if commit {
						n = "1"
					}
					definitionWireScalar(t, p.Ctx, peer, classAbsent, n)
					if commit {
						dropStatus := byte('I')
						if explicit {
							definitionWireSimple(t, p.Ctx, c, "BEGIN", []string{"BEGIN"}, 'T', nil)
							dropStatus = 'T'
						}
						definitionWireUtility(t, p, b, c, peer, prefix+"dropclass", `DROP OPERATOR CLASS public.tx_wire_class USING tx_wire_am RESTRICT`, "DROP OPERATOR CLASS", classAbsent, dropStatus, "1")
						definitionWireUtility(t, p, b, c, peer, prefix+"dropfamily", `DROP OPERATOR FAMILY public.tx_wire_family USING tx_wire_am RESTRICT`, "DROP OPERATOR FAMILY", familyAbsent, dropStatus, "1")
						definitionWireUtility(t, p, b, c, peer, prefix+"dropam", `DROP ACCESS METHOD tx_wire_am RESTRICT`, "DROP ACCESS METHOD", amAbsent, dropStatus, "1")
						if explicit {
							definitionWireScalar(t, p.Ctx, peer, amAbsent, "1")
							definitionWireSimple(t, p.Ctx, c, "COMMIT", []string{"COMMIT"}, 'I', nil)
						}
					}
					definitionWireScalar(t, p.Ctx, peer, amAbsent, "0")
				}
			}
			// Simple-query message retention: a late error rolls back every successful prefix.
			want := fixture.ControlsReferenceDiagnostic("42883", `function tx_wire_missing(internal) does not exist`)
			results, err := c.Exec(p.Ctx, `CREATE ACCESS METHOD tx_wire_am TYPE INDEX HANDLER bthandler; CREATE OPERATOR FAMILY public.tx_wire_family USING tx_wire_am; CREATE ACCESS METHOD tx_wire_failed TYPE INDEX HANDLER tx_wire_missing`).ReadAll()
			fixture.AssertDiagnostic(t, err, &want)
			if len(results) != 2 || results[0].CommandTag.String() != "CREATE ACCESS METHOD" || results[1].CommandTag.String() != "CREATE OPERATOR FAMILY" || c.TxStatus() != 'I' {
				t.Fatalf("retained failure tags/status: %#v %c", results, c.TxStatus())
			}
			definitionWireScalar(t, p.Ctx, peer, amAbsent, "0")
			definitionWireScalar(t, p.Ctx, peer, familyAbsent, "0")
			definitionWireSimple(t, p.Ctx, c, `BEGIN; CREATE ACCESS METHOD tx_wire_am TYPE INDEX HANDLER bthandler; CREATE OPERATOR FAMILY public.tx_wire_family USING tx_wire_am; SAVEPOINT member`, []string{"BEGIN", "CREATE ACCESS METHOD", "CREATE OPERATOR FAMILY", "SAVEPOINT"}, 'T', nil)
			definitionWireSimple(t, p.Ctx, c, `ALTER OPERATOR FAMILY public.tx_wire_family USING tx_wire_am ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`, []string{"ALTER OPERATOR FAMILY"}, 'T', nil)
			duplicate := fixture.ControlsReferenceDiagnostic("42710", `function 1(integer,integer) already exists in operator family "public.tx_wire_family"`)
			_, err = c.Exec(p.Ctx, `ALTER OPERATOR FAMILY public.tx_wire_family USING tx_wire_am ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer)`).ReadAll()
			fixture.AssertDiagnostic(t, err, &duplicate)
			if c.TxStatus() != 'E' {
				t.Fatal("semantic failure did not set Ready E")
			}
			aborted := definitionAborted()
			_, err = c.Exec(p.Ctx, `SELECT 1`).ReadAll()
			fixture.AssertDiagnostic(t, err, &aborted)
			definitionWireSimple(t, p.Ctx, c, `ROLLBACK TO member; ALTER OPERATOR FAMILY public.tx_wire_family USING tx_wire_am ADD FUNCTION 1 pg_catalog.btint4cmp(integer,integer); RELEASE member; ROLLBACK`, []string{"ROLLBACK", "ALTER OPERATOR FAMILY", "RELEASE", "ROLLBACK"}, 'I', nil)
			definitionWireScalar(t, p.Ctx, peer, amAbsent, "0")
			definitionWireSimple(t, p.Ctx, c, `CREATE ACCESS METHOD tx_wire_am TYPE INDEX HANDLER bthandler; CREATE OPERATOR FAMILY public.tx_wire_family USING tx_wire_am; CREATE OPERATOR CLASS public.tx_wire_class DEFAULT FOR TYPE integer USING tx_wire_am FAMILY public.tx_wire_family AS `+definitionClassItems+`; ALTER OPERATOR FAMILY public.tx_wire_family USING tx_wire_am ADD FUNCTION 2 (integer,integer) pg_catalog.btint4sortsupport(internal)`, []string{"CREATE ACCESS METHOD", "CREATE OPERATOR FAMILY", "CREATE OPERATOR CLASS", "ALTER OPERATOR FAMILY"}, 'I', nil)
			definitionWireScalar(t, p.Ctx, peer, `SELECT count(*) FROM pg_amproc a JOIN pg_opfamily f ON f.oid=a.amprocfamily WHERE f.opfname='tx_wire_family'`, "2")
			definitionWireSimple(t, p.Ctx, c, `ALTER OPERATOR FAMILY public.tx_wire_family USING tx_wire_am DROP FUNCTION 2(integer,integer); DROP OPERATOR CLASS public.tx_wire_class USING tx_wire_am; DROP OPERATOR FAMILY public.tx_wire_family USING tx_wire_am; DROP ACCESS METHOD tx_wire_am`, []string{"ALTER OPERATOR FAMILY", "DROP OPERATOR CLASS", "DROP OPERATOR FAMILY", "DROP ACCESS METHOD"}, 'I', nil)
			definitionWireScalar(t, p.Ctx, c, `SELECT 42`, "42")
			definitionWireAuthority(t, p, b, c, peer)
			definitionServerGoTransactions(t, p, b)
		})
	}
}
