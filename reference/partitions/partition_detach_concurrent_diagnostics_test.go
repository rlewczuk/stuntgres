package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionDetachConcurrentDiagnostics(t *testing.T) {
	cases := []struct {
		name, q        string
		setup          []string
		pending, block bool
		want           fixture.Diagnostic
		token          string
	}{
		{name: "transaction", q: "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY", block: true, want: fixture.Diagnostic{Code: "25001", Message: "ALTER TABLE ... DETACH CONCURRENTLY cannot run inside a transaction block"}},
		{name: "default", q: "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY", setup: []string{"CREATE TABLE dc_default PARTITION OF dc_parent DEFAULT"}, want: fixture.Diagnostic{Code: "55000", Message: "cannot detach partitions concurrently when a default partition exists"}},
		{name: "default_candidate", q: "ALTER TABLE dc_parent DETACH PARTITION dc_default CONCURRENTLY", setup: []string{"CREATE TABLE dc_default PARTITION OF dc_parent DEFAULT"}, want: fixture.Diagnostic{Code: "55000", Message: "cannot detach partitions concurrently when a default partition exists"}},
		{name: "pending_same", q: "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY", pending: true, want: fixture.Diagnostic{Code: "55000", Message: `partition "dc_leave" already pending detach in partitioned table "public.dc_parent"`, Hint: detachConcurrentHint}},
		{name: "pending_sibling", q: "ALTER TABLE dc_parent DETACH PARTITION dc_keep CONCURRENTLY", pending: true, want: fixture.Diagnostic{Code: "55000", Message: `partition "dc_leave" already pending detach in partitioned table "public.dc_parent"`, Hint: detachConcurrentHint}},
		{name: "ordinary_pending", q: "ALTER TABLE dc_parent DETACH PARTITION dc_leave", pending: true, want: fixture.Diagnostic{Code: "55000", Message: `cannot detach partition "dc_leave"`, Detail: "The partition is being detached concurrently or has an unfinished detach.", Hint: detachConcurrentHint}},
		{name: "alter_pending", q: "ALTER TABLE dc_leave ADD COLUMN extra INT", pending: true, want: fixture.Diagnostic{Code: "55000", Message: `cannot alter partition "dc_leave" with an incomplete detach`, Hint: detachConcurrentHint}},
		{name: "finalize_attached", q: "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", want: fixture.Diagnostic{Code: "55000", Message: `cannot complete detaching partition "dc_leave"`, Detail: "There's no pending concurrent detach."}},
		{name: "wrong_parent", q: "ALTER TABLE dc_other DETACH PARTITION dc_leave FINALIZE", setup: []string{"CREATE TABLE dc_other(k INT) PARTITION BY RANGE(k)"}, want: fixture.Diagnostic{Code: "42P01", Message: `relation "dc_leave" is not a partition of relation "dc_other"`}},
		{name: "ordinary_parent", q: "ALTER TABLE dc_regular DETACH PARTITION dc_leave FINALIZE", setup: []string{"CREATE TABLE dc_regular(k INT)"}, want: fixture.Diagnostic{Code: "42809", Message: `ALTER action DETACH PARTITION ... FINALIZE cannot be performed on relation "dc_regular"`, Detail: "This operation is not supported for tables."}},
		{name: "missing_parent", q: "ALTER TABLE dc_absent DETACH PARTITION dc_leave CONCURRENTLY", want: fixture.Diagnostic{Code: "42P01", Message: `relation "dc_absent" does not exist`}},
		{name: "missing_child", q: "ALTER TABLE dc_parent DETACH PARTITION dc_absent FINALIZE", want: fixture.Diagnostic{Code: "42P01", Message: `relation "dc_absent" does not exist`}},
		{name: "view", q: "ALTER TABLE dc_view DETACH PARTITION dc_leave CONCURRENTLY", setup: []string{"CREATE VIEW dc_view AS SELECT k FROM dc_leave"}, want: fixture.Diagnostic{Code: "42809", Message: `ALTER action DETACH PARTITION cannot be performed on relation "dc_view"`, Detail: "This operation is not supported for views."}},
		{name: "repeated", q: "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY CONCURRENTLY", want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "CONCURRENTLY"`}, token: "CONCURRENTLY CONCURRENTLY"},
		{name: "combined", q: "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY FINALIZE", want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "FINALIZE"`}, token: "FINALIZE"},
		{name: "reverse", q: "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE CONCURRENTLY", want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "CONCURRENTLY"`}, token: "CONCURRENTLY"},
		{name: "action_list", q: "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY, ADD COLUMN j INT", want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near ","`}, token: ","},
		{name: "trailing", q: "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE garbage", want: fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "garbage"`}, token: "garbage"},
	}
	for _, c := range cases {
		for _, held := range []bool{false, true} {
			t.Run(c.name+"/"+map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
				detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					detachConcurrentSetup(run)
					for _, q := range c.setup {
						run(fixture.Exec(q, 0))
					}
					if c.pending {
						detachConcurrentPending(t, p, b, held)
					}
					before := partitionDetachCapture(t, p, b.DB, "SELECT classid::BIGINT,objid::BIGINT,refclassid::BIGINT,refobjid::BIGINT,deptype FROM pg_depend ORDER BY classid,objid,refclassid,refobjid,deptype")
					owner := detachConcurrentConn(t, p, b)
					var e fixture.Executor = owner
					var tx *sql.Tx
					if c.block {
						var err error
						tx, err = owner.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						e = tx
					}
					want := c.want
					if c.token != "" {
						want.Position = referenceRangePosition(c.q, c.token)
						if c.name == "repeated" {
							want.Position = fixture.Position(len("ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY ") + 1)
						}
					}
					s := fixture.ImplicitError(c.q, want)
					s.ErrorStage = "execute"
					if held {
						stmt, err := e.PrepareContext(p.Ctx, c.q)
						if c.token != "" {
							want.Severity = "ERROR"
							want.SeverityUnlocalized = "ERROR"
							fixture.AssertDiagnostic(t, err, &want)
						} else {
							if err != nil {
								t.Fatal("expected execute-stage failure, prepare failed", err)
							}
							defer stmt.Close()
							fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: e, Statement: stmt, SQL: c.q}, b.Recorder, s)
						}
					} else {
						fixture.RunStep(t, p.Ctx, e, b.Recorder, s)
					}
					if tx != nil {
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
					}
					after := partitionDetachCapture(t, p, b.DB, "SELECT classid::BIGINT,objid::BIGINT,refclassid::BIGINT,refobjid::BIGINT,deptype FROM pg_depend ORDER BY classid,objid,refclassid,refobjid,deptype")
					if !reflect.DeepEqual(before, after) {
						t.Fatal("rejection changed dependencies", before, after)
					}
					detachConcurrentState(run, true, c.pending)
				})
			})
		}
	}
	detachConcurrentAdmission(t)
	detachConcurrentOwnership(t)
	detachConcurrentMetadata(t)
}

func detachConcurrentAdmission(t *testing.T) {
	for _, mode := range []string{"read_only", "aborted", "if_exists", "qualified", "extra_arguments"} {
		t.Run(mode, func(t *testing.T) {
			if mode == "qualified" {
				fixture.SchemaReferenceCleanup(t, "dc_names")
			}
			detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				detachConcurrentSetup(run)
				switch mode {
				case "qualified":
					run(fixture.Exec("CREATE SCHEMA dc_names", 0))
					run(fixture.Exec(`CREATE TABLE dc_names."P é"(k INT) PARTITION BY RANGE(k)`, 0))
					run(fixture.Exec(`CREATE TABLE dc_names."C é" PARTITION OF dc_names."P é" FOR VALUES FROM(0) TO(4)`, 0))
					detachConcurrentExecute(t, p, b, b.DB, `ALTER TABLE dc_names."P é" DETACH PARTITION dc_names."C é" CONCURRENTLY`, true)
					run(detachConcurrentGraph(`dc_names."P é"`, `dc_names."C é"`, false, false))
				case "if_exists":
					var notices []fixture.Diagnostic
					_, c, r := fixture.NoticeReferencePool(t, p, b.PG, &notices)
					fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ALTER TABLE IF EXISTS dc_absent DETACH PARTITION dc_missing CONCURRENTLY", 0))
					want := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "dc_absent" does not exist, skipping`}}
					if !reflect.DeepEqual(notices, want) {
						t.Fatal(notices)
					}
				case "extra_arguments":
					stmt, err := b.DB.PrepareContext(p.Ctx, "ALTER TABLE dc_parent DETACH PARTITION dc_leave CONCURRENTLY")
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					if _, err := stmt.ExecContext(p.Ctx, 1); err == nil {
						t.Fatal("extra argument accepted")
					}
					detachConcurrentState(run, true, false)
				default:
					tx, err := b.DB.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: mode == "read_only"})
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					if mode == "aborted" {
						fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError("SELECT 1/0", fixture.Diagnostic{Code: "22012", Message: "division by zero"}))
					}
					want := fixture.Diagnostic{Code: "25006", Message: "cannot execute ALTER TABLE in a read-only transaction"}
					if mode == "aborted" {
						want = fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}
					}
					fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ImplicitError("ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", want))
					tx.Rollback()
					detachConcurrentState(run, true, false)
				}
			})
		})
	}
}
