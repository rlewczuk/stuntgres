package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceTransactionalSchemaDropAdmission keeps the namespace AX wait real:
// CREATE in held owns namespace AS until A commits. In particular, later is
// absent when B captures DROP's names, not merely absent when B prepares it.
func TestReferenceTransactionalSchemaDropAdmission(t *testing.T) {
	for _, change := range []string{"absent_appears", "rename", "replacement", "authority", "cancel"} {
		t.Run(change, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"tx_drop_actor", "tx_drop_other"})
			fixture.SchemaReferenceCleanup(t, "tx_drop_held", "tx_drop_later", "tx_drop_moved")
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
					var notices []fixture.Diagnostic
					_, c, recorder := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					fixture.LockExec(t, p.Ctx, a, "CREATE SCHEMA tx_drop_held", 0)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_drop_held.root(id SERIAL PRIMARY KEY, v INT CHECK(v>0)); INSERT INTO tx_drop_held.root(v) VALUES(11)", 1)
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE public.tx_drop_child(id INT REFERENCES tx_drop_held.root(id)); INSERT INTO public.tx_drop_child VALUES(1)", 1)
					fixture.LockExec(t, p.Ctx, a, "CREATE VIEW public.tx_drop_view AS SELECT v FROM tx_drop_held.root", 0)
					if change == "authority" {
						fixture.LockExec(t, p.Ctx, a, "CREATE ROLE tx_drop_actor; CREATE ROLE tx_drop_other", 0)
						fixture.LockExec(t, p.Ctx, a, "ALTER SCHEMA tx_drop_held OWNER TO tx_drop_actor", 0)
						fixture.LockExec(t, p.Ctx, c, "SET ROLE tx_drop_actor", 0)
					}
					ap := transactionalSchemaPID(t, p, b, a)
					cp := transactionalSchemaPID(t, p, b, c)
					fixture.LockExec(t, p.Ctx, a, "BEGIN", 0)
					defer a.ExecContext(p.Ctx, "ROLLBACK")
					fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_drop_held.private_hold(v INT)", 0)
					fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
					defer c.ExecContext(p.Ctx, "ROLLBACK")
					query := "DROP SCHEMA IF EXISTS tx_drop_held, tx_drop_later CASCADE"
					// Reuse this actual prepared handle after recovery, rather than
					// treating a fresh Prepare as evidence of owner reuse.
					reuse, err := c.PrepareContext(p.Ctx, "SELECT 17::integer AS v")
					if err != nil {
						t.Fatal(err)
					}
					defer reuse.Close()
					ctx, cancel := context.WithCancel(p.Ctx)
					defer cancel()
					done := make(chan struct{})
					await := fixture.LockOperation(t, ctx, func(ctx context.Context) fixture.RelationOutcome {
						defer close(done)
						result, err := c.ExecContext(ctx, query)
						return fixture.RelationOutcome{Result: result, Err: err}
					})
					// Namespace resources are not relation pg_locks tags.
					// Require the real blocking edge on this backend's pool;
					// worker completion is never accepted as queue evidence.
					for blocked := false; !blocked; {
						select {
						case <-done:
							t.Fatal("DROP completed before its namespace wait was observed")
						default:
						}
						if err := b.DB.QueryRowContext(p.Ctx, "SELECT $1::integer = ANY(pg_blocking_pids($2::integer))", ap, cp).Scan(&blocked); err != nil {
							t.Fatal(err)
						}
					}
					var original, replacement int64
					var retained []string
					if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='tx_drop_held'").Scan(&original); err != nil {
						t.Fatal(err)
					}
					switch change {
					case "absent_appears":
						fixture.LockExec(t, p.Ctx, a, "CREATE SCHEMA tx_drop_later", 0)
						fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_drop_later.victim(id SERIAL PRIMARY KEY, v INT CHECK(v>0)); INSERT INTO tx_drop_later.victim(v) VALUES(23)", 1)
					case "rename", "replacement":
						fixture.LockExec(t, p.Ctx, a, "ALTER SCHEMA tx_drop_held RENAME TO tx_drop_moved", 0)
						retained = transactionalSchemaDropGraph(t, p.Ctx, a)
						if change == "replacement" {
							fixture.LockExec(t, p.Ctx, a, "CREATE SCHEMA tx_drop_held", 0)
							fixture.LockExec(t, p.Ctx, a, "CREATE TABLE tx_drop_held.victim(id SERIAL PRIMARY KEY, v INT CHECK(v>0)); INSERT INTO tx_drop_held.victim(v) VALUES(29)", 1)
							if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_namespace WHERE nspname='tx_drop_held'").Scan(&replacement); err != nil || replacement == original {
								t.Fatalf("namespace replacement: old=%d new=%d err=%v", original, replacement, err)
							}
						}
					case "authority":
						fixture.LockExec(t, p.Ctx, a, "ALTER SCHEMA tx_drop_held OWNER TO tx_drop_other", 0)
					}
					// Capture the entire relevant object/dependency identity bag
					// through A's private view before releasing its namespace hold.
					before := transactionalSchemaDropGraph(t, p.Ctx, a)
					if change == "cancel" {
						if b.PG {
							var canceled bool
							if err := b.DB.QueryRowContext(p.Ctx, "SELECT pg_cancel_backend($1)", cp).Scan(&canceled); err != nil || !canceled {
								t.Fatal(canceled, err)
							}
						} else {
							cancel()
						}
					} else {
						fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
					}
					outcome := await()
					cancel()
					pgSuccess := b.PG && change != "cancel" && change != "authority"
					if pgSuccess {
						fixture.State(t, outcome.Err, "")
						if outcome.Result == nil {
							t.Fatal("successful DROP returned no result")
						}
						if count, err := outcome.Result.RowsAffected(); err != nil || count != 0 {
							t.Fatal("DROP count", count, err)
						}
						missing := func(name string) fixture.Diagnostic {
							return fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: fmt.Sprintf("schema %q does not exist, skipping", name)}
						}
						var want []fixture.Diagnostic
						switch change {
						case "absent_appears":
							want = []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000",
								Message: "drop cascades to 5 other objects",
								Detail:  "drop cascades to table tx_drop_later.victim\ndrop cascades to table tx_drop_held.root\ndrop cascades to constraint tx_drop_child_id_fkey on table tx_drop_child\ndrop cascades to view tx_drop_view\ndrop cascades to table tx_drop_held.private_hold"}}
						case "rename":
							want = []fixture.Diagnostic{missing("tx_drop_held"), missing("tx_drop_later")}
						case "replacement":
							want = []fixture.Diagnostic{missing("tx_drop_later"), {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: "drop cascades to table tx_drop_held.victim"}}
						}
						if !reflect.DeepEqual(notices, want) {
							t.Fatalf("DROP notices=%#v want=%#v", notices, want)
						}
						fixture.LockExec(t, p.Ctx, c, "COMMIT", 0)
					} else {
						switch change {
						case "cancel":
							if b.PG {
								d := fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to user request")
								fixture.AssertDiagnostic(t, outcome.Err, &d)
							} else {
								fixture.TransactionOrdinary(t, outcome.Err, context.Canceled.Error())
							}
						case "authority":
							d := fixture.ControlsReferenceDiagnostic("42501", `must be owner of schema tx_drop_held`)
							fixture.AssertDiagnostic(t, outcome.Err, &d)
						default:
							fixture.TransactionOrdinary(t, outcome.Err, "stuntgres: catalog replaced before publication")
						}
						if len(notices) != 0 {
							t.Fatalf("rejected DROP emitted notices: %#v", notices)
						}
						aborted := fixture.ErrorDiagnostic("SELECT 17::integer AS v", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block"))
						aborted.ErrorStage = "execute"
						fixture.RunStep(t, p.Ctx, c, recorder, aborted)
						fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
					}
					if change == "cancel" {
						fixture.LockExec(t, p.Ctx, a, "COMMIT", 0)
					}
					expected := before
					if pgSuccess && change == "replacement" {
						expected = retained
					}
					if !(pgSuccess && change == "absent_appears") {
						if after := transactionalSchemaDropGraph(t, p.Ctx, a); !reflect.DeepEqual(after, expected) {
							t.Fatalf("DROP changed retained namespace/object/dependency graph:\nbefore=%v\nafter=%v", expected, after)
						}
					}
					root := "tx_drop_held"
					if change == "rename" || change == "replacement" {
						root = "tx_drop_moved"
					}
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
					if !(pgSuccess && change == "absent_appears") {
						run(fixture.Select("SELECT v FROM "+root+".root", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
						run(fixture.Select("SELECT v FROM public.tx_drop_view", []string{"v"}, []string{"INT4"}, [][]any{{int64(11)}}))
					}
					run(fixture.Select("SELECT id FROM public.tx_drop_child", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
					if !pgSuccess && (change == "absent_appears" || change == "replacement") {
						ns, value := "tx_drop_later", int64(23)
						if change == "replacement" {
							ns, value = "tx_drop_held", 29
						}
						run(fixture.Select("SELECT v FROM "+ns+".victim", []string{"v"}, []string{"INT4"}, [][]any{{value}}))
					}
					if pgSuccess {
						run(fixture.Select("SELECT count(*) FROM pg_namespace WHERE nspname IN ('tx_drop_held','tx_drop_later')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
						if change == "absent_appears" {
							run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname IN ('root','root_id_seq','root_pkey','victim','victim_id_seq','victim_pkey','private_hold','tx_drop_view')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							run(fixture.Select("SELECT count(*) FROM pg_type WHERE typname IN ('root','_root','victim','_victim','private_hold','_private_hold','tx_drop_view','_tx_drop_view')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							run(fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='public.tx_drop_child'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
							run(fixture.Select("SELECT count(*) FROM pg_depend WHERE classid='pg_class'::regclass AND objid='public.tx_drop_child'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
						}
					}
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: reuse, SQL: "SELECT 17::integer AS v"}, recorder,
						fixture.Select("SELECT 17::integer AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(17)}}))
					fixture.LockExec(t, p.Ctx, c, "BEGIN", 0)
					fixture.LockExec(t, p.Ctx, c, "ROLLBACK", 0)
				})
			}
		})
	}
}

// Compare actual catalog identities, ACLs and dependency edges, not allocator
// equality across backends. All snapshots are inside one fresh backend scenario.
func transactionalSchemaDropGraph(t *testing.T, ctx context.Context, conn *sql.Conn) []string {
	t.Helper()
	var bag []string
	for _, query := range []string{
		"SELECT oid,nspname,nspowner,nspacl FROM pg_namespace WHERE nspname IN ('tx_drop_held','tx_drop_later','tx_drop_moved') ORDER BY oid",
		"SELECT c.oid,c.relname,c.relnamespace,c.reltype,c.relowner,c.relacl FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ('tx_drop_held','tx_drop_later','tx_drop_moved') OR (n.nspname='public' AND c.relname IN ('tx_drop_child','tx_drop_view')) ORDER BY c.oid",
		"SELECT t.oid,t.typname,t.typnamespace,t.typrelid FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname IN ('tx_drop_held','tx_drop_later','tx_drop_moved') ORDER BY t.oid",
		"SELECT c.oid,c.conname,c.conrelid,c.confrelid,c.conindid FROM pg_constraint c JOIN pg_namespace n ON n.oid=c.connamespace WHERE n.nspname IN ('tx_drop_held','tx_drop_later','tx_drop_moved') OR c.conrelid='public.tx_drop_child'::regclass ORDER BY c.oid",
		"SELECT classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype FROM pg_depend WHERE objid IN (SELECT c.oid FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname IN ('tx_drop_held','tx_drop_later','tx_drop_moved') OR (n.nspname='public' AND c.relname IN ('tx_drop_child','tx_drop_view'))) OR refobjid IN (SELECT oid FROM pg_namespace WHERE nspname IN ('tx_drop_held','tx_drop_later','tx_drop_moved')) ORDER BY classid,objid,objsubid,refclassid,refobjid,refobjsubid,deptype",
	} {
		rows, err := conn.QueryContext(ctx, query)
		if err != nil {
			t.Fatal(query, err)
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			for i := range values {
				values[i] = fixture.Normalize(values[i])
			}
			bag = append(bag, fmt.Sprint(values))
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return bag
}
