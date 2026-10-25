package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDomainIdentityAtomicity(t *testing.T) {
	domainIdentityScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, notices *[]fixture.Diagnostic) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		domainIdentityDDL(t, p, c, r, "CREATE SCHEMA di_src", "CREATE SCHEMA di_dst", "CREATE DOMAIN di_src.d AS integer", "CREATE DOMAIN di_src.free AS integer", "CREATE TABLE di_src.t(v di_src.d,keep integer)")
		run(fixture.Exec("INSERT INTO di_src.t VALUES(7,11),(NULL,12)", 2))
		oid := domainAlterOID(t, p, c, "SELECT 'di_src.d'::regtype::oid")
		array := domainAlterOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='di_src.d'::regtype::oid")
		selectQ := "SELECT v,keep FROM di_src.t ORDER BY keep"
		consumer, err := c.PrepareContext(p.Ctx, selectQ)
		if err != nil {
			t.Fatal(err)
		}
		defer consumer.Close()
		check := func() {
			domainIdentitySameOID(t, p, c, "SELECT 'di_src.d'::regtype::oid", oid)
			domainIdentitySameOID(t, p, c, "SELECT typarray FROM pg_type WHERE oid='di_src.d'::regtype::oid", array)
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: consumer, SQL: selectQ}, r, fixture.Select(selectQ, []string{"v", "keep"}, []string{"INT4", "INT4"}, [][]any{{int64(7), int64(11)}, {nil, int64(12)}}))
		}
		// Native precancellation is an ordinary context error. Do not infer a
		// stable pq transport outcome from a race-prone precanceled request.
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, q := range []string{"ALTER DOMAIN di_src.d RENAME TO canceled", "ALTER DOMAIN di_src.d SET SCHEMA di_dst", "ALTER DOMAIN di_src.d OWNER TO CURRENT_USER", "DROP DOMAIN di_src.free,di_src.d CASCADE"} {
				result, err := c.ExecContext(ctx, q)
				if !errors.Is(err, context.Canceled) || result != nil {
					t.Fatalf("precanceled %s: result=%v error=%v", q, result, err)
				}
				check()
			}
		}
		writer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer writer.Close()
		reader, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		waiterPID, holderPID := 0, 0
		var tableOID int64
		if b.PG {
			waiterPID, holderPID = fixture.RowPIDs(t, p.Ctx, c, writer)
			tableOID = fixture.LockOID(t, p.Ctx, "di_src.t")
		}
		tx, err := writer.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		fixture.RunStep(t, p.Ctx, tx, r, fixture.Exec("INSERT INTO di_src.t VALUES(8,13)", 1))
		// DROP CASCADE must acquire the actual dependent relation lock. This
		// is not a presumption that rename/move take validation's ShareLock.
		q := "DROP DOMAIN di_src.free,di_src.d CASCADE"
		*notices = nil
		fixture.BinaryConversionActiveCancelAfter(t, p, b, c, q, "(*relationRequest).wait", func(ctx context.Context) {
			if b.PG {
				fixture.LockWaitBehind(t, ctx, waiterPID, holderPID, tableOID, "AccessExclusiveLock")
			}
			// A new AccessShareLock reader would queue behind the pending
			// AccessExclusiveLock and deadlock this cancellation callback.
			// Observe unpublished catalog identities instead; read table rows
			// with the SAME held consumer after cancellation has been joined.
			fixture.RunStep(t, ctx, reader, b.Recorder, fixture.Select("SELECT t.typname,n.nspname FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='di_src' AND t.typname IN ('d','_d','free','_free') ORDER BY t.typname", []string{"typname", "nspname"}, []string{"NAME", "NAME"}, [][]any{{"_d", "di_src"}, {"_free", "di_src"}, {"d", "di_src"}, {"free", "di_src"}}))
		}, func(ctx context.Context) fixture.RelationOutcome { return fixture.RowExecute(ctx, c, q, false) })
		check()
		domainAlterOID(t, p, c, "SELECT 'di_src.free'::regtype::oid")
		// Failed publication emits no CASCADE notice, including pinned18.6
		// cancellation before the dependent relation lock can be acquired.
		checkNotices := func() {
			if len(*notices) != 0 {
				t.Fatalf("unpublished cascade notices=%#v", *notices)
			}
			*notices = nil
		}
		checkNotices()
		domainIdentityDDL(t, p, c, r, "SET lock_timeout='100ms'")
		run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")))
		checkNotices()
		domainIdentityDDL(t, p, c, r, "SET lock_timeout=0")
		check()
		if err := tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		domainIdentityDDL(t, p, c, r, "ALTER DOMAIN di_src.d RENAME TO renamed", "ALTER DOMAIN di_src.renamed SET SCHEMA di_dst")
		domainIdentitySameOID(t, p, c, "SELECT 'di_dst.renamed'::regtype::oid", oid)
	})
}

func TestReferenceDomainIdentityBoundaries(t *testing.T) {
	domainIdentityScenario(t, func(t *testing.T, p *fixture.Pair, _ fixture.Backend, c *sql.Conn, r *fixture.Recorder, _ *[]fixture.Diagnostic) {
		domainIdentityDDL(t, p, c, r, "CREATE SCHEMA di_src", "CREATE SCHEMA di_dst", "CREATE DOMAIN di_src.d AS integer")
		oid := domainAlterOID(t, p, c, "SELECT 'di_src.d'::regtype::oid")
		for _, q := range []string{"ALTER DOMAIN di_src.d RENAME TO renamed", "ALTER DOMAIN di_src.d SET SCHEMA di_dst", "ALTER DOMAIN di_src.d OWNER TO CURRENT_USER", "DROP DOMAIN di_src.d"} {
			t.Run(q, func(t *testing.T) {
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				stmt, err := tx.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal("valid utility Prepare must be quiet", err)
				}
				defer stmt.Close()
				domainIdentitySameOID(t, p, tx, "SELECT 'di_src.d'::regtype::oid", oid)
				held := fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}
				s := fixture.Exec(q, 0)
				fixture.RunStep(t, p.Ctx, held, r, s)
				target := "di_src.d"
				switch q {
				case "ALTER DOMAIN di_src.d RENAME TO renamed":
					target = "di_src.renamed"
				case "ALTER DOMAIN di_src.d SET SCHEMA di_dst":
					target = "di_dst.d"
				}
				if q == "DROP DOMAIN di_src.d" {
					fixture.RunStep(t, p.Ctx, tx, r, fixture.Select("SELECT count(*) FROM pg_type WHERE oid=$1::oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, oid))
				} else {
					domainIdentitySameOID(t, p, tx, "SELECT '"+target+"'::regtype::oid", oid)
				}
				if q == "ALTER DOMAIN di_src.d OWNER TO CURRENT_USER" {
					fixture.RunStep(t, p.Ctx, tx, r, fixture.Select("SELECT t.typowner=(SELECT oid FROM pg_roles WHERE rolname=current_user) AS unchanged_owner FROM pg_type t WHERE t.oid='di_src.d'::regtype::oid", []string{"unchanged_owner"}, []string{"BOOL"}, [][]any{{true}}))
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				domainIdentitySameOID(t, p, c, "SELECT 'di_src.d'::regtype::oid", oid)
				tx, err = c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				verb := "ALTER DOMAIN"
				if q == "DROP DOMAIN di_src.d" {
					verb = "DROP DOMAIN"
				}
				s = fixture.Prepared(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+verb+" in a read-only transaction")))
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, tx, r, s)
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				domainIdentitySameOID(t, p, c, "SELECT 'di_src.d'::regtype::oid", oid)
			})
		}
	})
}
