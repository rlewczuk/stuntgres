package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

// Each phase has fresh catalogs. Selection pins the execution-time source
// independently of exclusion, so a timeout cannot masquerade as source binding.
func TestReferenceTypedTableAttachmentSourceAdmission(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			for _, phase := range []string{"selection", "timeout", "cancellation"} {
				t.Run(phase, func(t *testing.T) {
					fixture.SchemaReferenceCleanup(t, "earlier_schema")
					p := fixture.NewScenario(t)
					for _, b := range p.Backends() {
						t.Run(b.Name, func(t *testing.T) {
							typedAttachmentSourceAdmission(t, p, b, prepared, phase)
						})
					}
				})
			}
		})
	}
}

func typedAttachmentSourceAdmission(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool, phase string) {
	ctx, cancel := context.WithTimeout(p.Ctx, 20*time.Second)
	defer cancel()
	conn := func() *sql.Conn {
		c, err := b.DB.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	admin, attachment := conn(), conn()
	run := func(s fixture.Step) { fixture.RunStep(t, ctx, admin, b.Recorder, s) }
	run(fixture.Exec("CREATE SCHEMA earlier_schema", 0))
	run(fixture.Exec("CREATE TYPE public.pair AS (id integer,note varchar(5))", 0))
	run(fixture.Exec("CREATE TABLE public.t (id integer,note varchar(5))", 0))
	run(fixture.Exec("INSERT INTO public.t VALUES(11,'hey')", 1))
	fixture.RunStep(t, ctx, attachment, b.Recorder, fixture.Exec("SET search_path=earlier_schema,public", 0))
	fixture.RunStep(t, ctx, attachment, b.Recorder, fixture.Exec("SET lock_timeout='3s'", 0))
	run(fixture.Exec("SET lock_timeout='50ms'", 0))

	hold := func(table string) *sql.Tx {
		tx, err := b.DB.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback() })
		fixture.RunStep(t, ctx, tx, b.Recorder, typedTablePairRows(table, [][]any{{int64(11), "hey"}}))
		return tx
	}
	targetReader := hold("public.t")

	type outcome struct {
		result sql.Result
		err    error
	}
	start := func(c *sql.Conn, query string) (context.CancelFunc, func() outcome, chan outcome) {
		var stmt *sql.Stmt
		if prepared {
			var err error
			stmt, err = c.PrepareContext(ctx, query)
			if err != nil {
				t.Fatal("quiet utility Prepare", err)
			}
			t.Cleanup(func() { _ = stmt.Close() })
		}
		workerCtx, stop := context.WithTimeout(ctx, 10*time.Second)
		done := make(chan outcome, 1)
		go func() {
			var o outcome
			if stmt != nil {
				o.result, o.err = stmt.ExecContext(workerCtx)
			} else {
				o.result, o.err = c.ExecContext(workerCtx, query)
			}
			done <- o
		}()
		joined := false
		var observed outcome
		join := func() outcome {
			if !joined {
				select {
				case observed = <-done:
					joined = true
				case <-time.After(12 * time.Second):
					t.Fatal("bounded utility worker did not drain")
				}
			}
			return observed
		}
		// Registered after connection/transaction cleanup, so cancellation and
		// joining always precede releasing the retained readers and connections.
		t.Cleanup(func() { stop(); join() })
		return stop, join, done
	}
	wantTimeout := typedTableError("55P03", "canceling statement due to lock timeout")
	queued := func(table string, done chan outcome) {
		probeCtx, stop := context.WithTimeout(ctx, 2*time.Second)
		defer stop()
		query := "SELECT id FROM " + table + " LIMIT 0"
		want := wantTimeout
		want.Position = "16"
		for {
			rows, err := admin.QueryContext(probeCtx, query)
			if rows != nil {
				if err := rows.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err != nil {
				fixture.AssertDiagnostic(t, err, &want)
				return
			}
			select {
			case o := <-done:
				// Preserve the result for the registered join even on failure.
				done <- o
				t.Fatalf("utility completed before retained-reader release: result=%v error=%v", o.result, o.err)
			default:
			}
			if probeCtx.Err() != nil {
				t.Fatal("utility never excluded the fairness probe", probeCtx.Err())
			}
		}
	}
	stopAttachment, joinAttachment, attachmentDone := start(attachment, "ALTER TABLE public.t OF pair")
	queued("public.t", attachmentDone)
	// Only now does the earlier-path shadow exist: admission already waited
	// behind a retained reader of the ordinary target.
	run(fixture.Exec("CREATE TYPE earlier_schema.pair AS (id integer,note varchar(5))", 0))
	identities := func() [4]int64 {
		var ids [4]int64
		err := admin.QueryRowContext(ctx, "SELECT 'public.t'::regclass::oid,'public.t'::regtype::oid,'public.pair'::regtype::oid,'earlier_schema.pair'::regtype::oid").Scan(&ids[0], &ids[1], &ids[2], &ids[3])
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if id <= 0 {
				t.Fatal("nonpositive retained provider identity", ids)
			}
		}
		if ids[1] == ids[2] || ids[1] == ids[3] || ids[2] == ids[3] {
			t.Fatal("target own-row and standalone source identities overlap", ids)
		}
		return ids
	}
	before := identities()
	sourceEdges := func(attached bool) {
		var edges [][]any
		if attached {
			edges = [][]any{{true, "n"}}
		}
		s := fixture.Select("SELECT refobjid='earlier_schema.pair'::regtype::oid AS shadow,deptype FROM pg_depend WHERE classid='pg_class'::regclass::oid AND objid='public.t'::regclass::oid AND objsubid=0 AND refclassid=1247::oid AND refobjsubid=0 AND (refobjid='public.pair'::regtype::oid OR refobjid='earlier_schema.pair'::regtype::oid)", []string{"shadow", "deptype"}, []string{"BOOL", "CHAR"}, edges)
		s.Bag = true
		run(s)
	}

	if phase != "selection" {
		run(fixture.Exec("CREATE TABLE public.child OF earlier_schema.pair", 0))
		run(fixture.Exec("INSERT INTO public.child VALUES(11,'hey')", 1))
		childReader := hold("public.child")
		writer := conn()
		stopWriter, joinWriter, writerDone := start(writer, "ALTER TYPE earlier_schema.pair ADD ATTRIBUTE flag boolean CASCADE")
		queued("public.child", writerDone)
		// This qualified CREATE is an independent public source-AS probe. The
		// writer has source AX while waiting for the retained child's reader.
		probe := fixture.ErrorDiagnostic("CREATE TABLE public.source_probe OF earlier_schema.pair", wantTimeout)
		probe.ErrorStage = "execute"
		probe.Prepared = prepared
		run(probe)
		if err := targetReader.Rollback(); err != nil {
			t.Fatal(err)
		}
		if phase == "cancellation" {
			// With the original reader gone, this exclusion proves attachment
			// still owns target AX while waiting on its actual selected source.
			queued("public.t", attachmentDone)
			stopAttachment()
		}
		o := joinAttachment()
		if o.result != nil || o.err == nil {
			t.Fatal("blocked attachment exposed success", o.result, o.err)
		}
		if phase == "timeout" {
			fixture.AssertDiagnostic(t, o.err, &wantTimeout)
		} else if b.PG {
			want := typedTableError("57014", "canceling statement due to user request")
			fixture.AssertDiagnostic(t, o.err, &want)
		} else if !errors.Is(o.err, context.Canceled) {
			t.Fatal("actual source wait did not honor cancellation", o.err)
		}
		stopWriter()
		if o := joinWriter(); o.err == nil || o.result != nil {
			t.Fatal("blocked source writer exposed success", o.result, o.err)
		}
		if err := childReader.Rollback(); err != nil {
			t.Fatal(err)
		}
		run(typedTableAttachment("public.t", "public.pair", false))
		sourceEdges(false)
		run(typedTableAttachment("public.child", "earlier_schema.pair", true))
		run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='source_probe'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(typedTablePairRows("public.child", [][]any{{int64(11), "hey"}}))
	} else {
		if err := targetReader.Rollback(); err != nil {
			t.Fatal(err)
		}
		o := joinAttachment()
		if o.err != nil || o.result == nil {
			t.Fatal("source-selection attachment failed", o.err)
		}
		if count, err := o.result.RowsAffected(); err != nil || count != 0 {
			t.Fatal("attachment utility count", count, err)
		}
		// PostgreSQL18 selects the execution-time earlier-path source, not
		// the source visible when target admission first began waiting.
		run(typedTableAttachment("public.t", "earlier_schema.pair", true))
		sourceEdges(true)
	}
	stopAttachment()
	if after := identities(); after != before {
		t.Fatal("source wait replaced retained provider identities", before, after)
	}
	run(typedTablePairRows("public.t", [][]any{{int64(11), "hey"}}))
	fixture.TableDDLMetadata(t, ctx, admin, b.PG, prepared, "SELECT * FROM public.t LIMIT 0", []string{"id", "note"}, []string{"INT4", "VARCHAR"}, []bool{true, true}, 0)
	run(fixture.Exec("SET lock_timeout=0", 0))
	fresh := fixture.Exec("ALTER TABLE public.t OF earlier_schema.pair", 0)
	fresh.Prepared = prepared
	run(fresh)
	run(typedTableAttachment("public.t", "earlier_schema.pair", true))
	sourceEdges(true)
	run(fixture.Exec("ALTER TYPE earlier_schema.pair ADD ATTRIBUTE flag boolean CASCADE", 0))
	run(fixture.Select("SELECT * FROM public.t", []string{"id", "note", "flag"}, []string{"INT4", "VARCHAR", "BOOL"}, [][]any{{int64(11), "hey", nil}}))
	run(fixture.Exec("ALTER TABLE public.t NOT OF", 0))
	run(typedTableAttachment("public.t", "earlier_schema.pair", false))
	sourceEdges(false)
	run(fixture.Select("SELECT * FROM public.t", []string{"id", "note", "flag"}, []string{"INT4", "VARCHAR", "BOOL"}, [][]any{{int64(11), "hey", nil}}))
}
