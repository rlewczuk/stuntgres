package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func hstoreRecordRenamedVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor) {
	t.Helper()
	hstoreRecordStoredVerify(t, p, b, c)
	hstoreRecordIdentity(t, p, b, c, "hs_record_current.make_record", "hs_record_current.replace_record")
	hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT hs_record_current.make_record($1::hs_record_pair)::text AS v", `"i"=>"8", "n"=>"new"`, "(8,new)"))
	hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT hs_record_current.replace_record($1::hs_record_pair,$2::hstore)::text AS v", "(51,new)", "(8,new)", "i=>51"))
	hstoreRecordBoth(t, p, b, c, hstoreRecordText("SELECT ($1::hs_record_pair #= $2::hstore)::text AS v", "(51,new)", "(8,new)", "i=>51"))
	hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_record_stored(id) VALUES (4)", 1))
	hstoreRun(t, p, b, c, fixture.Select("SELECT r::text AS r,o::text AS o,h::text AS h FROM hs_record_stored WHERE id=4", []string{"r", "o", "h"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"(42,keep)", "(7,)", `"i"=>NULL, "n"=>NULL`}}))
	hstoreRun(t, p, b, c, fixture.Exec("DELETE FROM hs_record_stored WHERE id=4", 1))
}

func hstoreRecordReplayDump(t *testing.T, p *fixture.Pair, dump []byte, renamed bool) {
	t.Helper()
	if p.Postgres == nil {
		return
	}
	c, err := p.Postgres.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	clean := "DROP SCHEMA public CASCADE; CREATE SCHEMA public"
	if renamed {
		clean = "DROP SCHEMA IF EXISTS hs_record_current CASCADE; " + clean
	}
	if _, err := c.ExecContext(p.Ctx, clean); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, clean); err != nil {
			t.Error(err)
		}
	}()
	// Replay exactly the public native Dump bytes, including owner directives.
	if _, err := c.ExecContext(p.Ctx, string(dump)); err != nil {
		t.Fatalf("actual emitted record SQL replay: %v", err)
	}
	pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
	if renamed {
		hstoreRecordRenamedVerify(t, p, pg, c)
	} else {
		hstoreRecordStoredVerify(t, p, pg, c)
		hstoreRecordIdentity(t, p, pg, c, "hstore", "populate_record")
		hstoreRecordFreshCalls(t, p, pg, c)
	}
}

func TestReferenceHstoreRecordReplay(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "hs_record_current")
	hstoreRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreRecordSeed(t, p, b, c)
		hstoreRun(t, p, b, c, fixture.Exec("CREATE SCHEMA hs_record_current", 0))
		q := "SELECT (r #= 'i=>51'::hstore)::text AS v FROM hs_record_stored WHERE id=1"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		hstoreRun(t, p, b, held, hstoreRecordText(q, "(51,keep)"))
		for _, tc := range []struct{ name, signature, renamed string }{
			{"hstore", "record", "make_record"},
			{"populate_record", "anyelement,hstore", "replace_record"},
		} {
			var before, after int64
			if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid", tc.name+"("+tc.signature+")").Scan(&before); err != nil {
				t.Fatal(err)
			}
			hstoreRun(t, p, b, c, fixture.Exec("ALTER FUNCTION "+tc.name+"("+tc.signature+") RENAME TO "+tc.renamed, 0))
			hstoreRun(t, p, b, c, fixture.Exec("ALTER FUNCTION "+tc.renamed+"("+tc.signature+") SET SCHEMA hs_record_current", 0))
			if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid", "hs_record_current."+tc.renamed+"("+tc.signature+")").Scan(&after); err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Fatalf("renaming replaced record provider %d -> %d", before, after)
			}
		}
		hstoreRecordRenamedVerify(t, p, b, c)
		hstoreRun(t, p, b, held, hstoreRecordText(q, "(51,keep)"))
		if b.PG {
			return
		} // Native Dump/Restore/replay phase, not a second SQL scenario.
		dump := hstoreDump(t, p, p.Native)
		destination := fixture.NewFixture(t, false, true)
		if _, err := destination.Native.ExecContext(p.Ctx, "CREATE TABLE hs_record_displaced(v integer); CREATE EXTENSION hstore"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump)); err != nil {
			t.Fatal(err)
		}
		target := fixture.Backend{DB: destination.Memory, Recorder: destination.MemoryRecord}
		hstoreRecordRenamedVerify(t, p, target, destination.Memory)
		if !bytes.Equal(dump, hstoreDump(t, p, destination.Native)) {
			t.Fatal("renamed record dump changed after displaced Restore")
		}
		hstoreRecordReplayDump(t, p, dump, true)
	})
}
