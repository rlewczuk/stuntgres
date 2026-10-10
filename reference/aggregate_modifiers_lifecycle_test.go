package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"stuntgres"
	"testing"
)

func TestReferenceAggregateModifiersLifecycle(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_life(id INT,v TEXT)", 0))
		run(referenceExec("INSERT INTO am_life VALUES(1,'a'),(2,'a'),(3,'b'),(4,NULL)", 4))
		run(referenceExec("CREATE VIEW am_view AS SELECT count(*) FILTER(WHERE id>1) AS n,string_agg(DISTINCT v,',' ORDER BY v DESC) FILTER(WHERE id>1) AS id,count(*) FILTER(WHERE EXISTS(SELECT 1 FROM am_life q WHERE q.id=1) AND 'am_life'::regclass IS NOT NULL) AS dependency FROM am_life", 0))
		run(referenceExec("ALTER TABLE am_life RENAME TO am_renamed", 0))
		run(referenceExec("ALTER TABLE am_renamed RENAME COLUMN id TO source_id", 0))
		q := "SELECT n,id,dependency FROM am_view"
		s := referenceSelect(q, []string{"n", "id", "dependency"}, []string{"INT8", "TEXT", "INT8"}, [][]any{{int64(3), "b,a", int64(4)}})
		aggregateModifiersBoth(run, s)
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		referenceRunStep(t, p.ctx, held, b.recorder, s)
		var definition string
		if err := c.QueryRowContext(p.ctx, "SELECT pg_get_viewdef('am_view'::regclass,true)").Scan(&definition); err != nil {
			t.Fatal(err)
		}
		run(referenceExec("CREATE VIEW am_replay AS "+definition, 0))
		replay := s
		replay.query = "SELECT n,id,dependency FROM am_replay"
		run(replay)
		if !b.pg {
			old, err := stmt.QueryContext(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer old.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			if !old.Next() {
				t.Fatal(old.Err())
			}
			var n, dependency int64
			var text string
			if err := old.Scan(&n, &text, &dependency); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual([]any{n, text, dependency}, s.rows[0]) {
				t.Fatalf("retained row %d %q %d", n, text, dependency)
			}
			if old.Next() || old.Err() != nil {
				t.Fatal(old.Err())
			}
			old.Close()
			run(s)
			run(replay)
			referenceRunStep(t, p.ctx, held, b.recorder, s)
			// Restoring the same immutable view twice rebinds fresh descriptors.
			if err := stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, p.ctx, held, b.recorder, s)
			ctx, cancel := context.WithCancel(p.ctx)
			cancel()
			rows, err := stmt.QueryContext(ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled modifier query published cursor")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled modifier query: %v", err)
			}
			result, err := c.ExecContext(ctx, "INSERT INTO am_renamed SELECT count(DISTINCT source_id),'bad' FROM am_renamed")
			if result != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled INSERT %v %v", result, err)
			}
		}
		run(referenceExec("INSERT INTO am_renamed VALUES(5,'c')", 1))
		changed := s
		changed.rows = [][]any{{int64(4), "c,b,a", int64(5)}}
		referenceRunStep(t, p.ctx, held, b.recorder, changed)
		run(referenceSelect("SELECT count(DISTINCT source_id) FROM am_renamed", []string{"count"}, []string{"INT8"}, [][]any{{int64(5)}}))
	})
}

func TestReferenceAggregateModifiersPrivileges(t *testing.T) {
	roleReferenceCleanup(t, []string{"am_modifier_reader"})
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_acl(v INT,hidden INT)", 0))
		run(referenceExec("INSERT INTO am_acl VALUES(1,1)", 1))
		run(referenceExec("CREATE ROLE am_modifier_reader", 0))
		if b.pg { run(referenceExec("GRANT USAGE ON SCHEMA public TO am_modifier_reader", 0)) }
		run(referenceExec("GRANT SELECT(v) ON am_acl TO am_modifier_reader", 0))
		defer func() {
			if _, err := c.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION"); err != nil {
				t.Error(err)
			}
		}()
		run(referenceExec("SET SESSION AUTHORIZATION am_modifier_reader", 0))
		for _, q := range []string{
			"SELECT count(v) FILTER(WHERE hidden>0) FROM am_acl WHERE FALSE",
			"SELECT count(v ORDER BY hidden) FILTER(WHERE FALSE) FROM am_acl",
		} {
			for _, prepared := range []bool{false, true} {
				s := aggregateModifiersError(q, "42501", "permission denied for table am_acl", "")
				s.prepared = prepared
				run(s)
			}
		}
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("GRANT SELECT(hidden) ON am_acl TO am_modifier_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION am_modifier_reader", 0))
		q := "SELECT count(v ORDER BY hidden) FILTER(WHERE hidden>0) FROM am_acl"
		stmt, err := c.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
		referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(q, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
		run(referenceExec("RESET SESSION AUTHORIZATION", 0))
		run(referenceExec("REVOKE SELECT(hidden) ON am_acl FROM am_modifier_reader", 0))
		run(referenceExec("SET SESSION AUTHORIZATION am_modifier_reader", 0))
		referenceRunStep(t, p.ctx, held, b.recorder, aggregateModifiersError(q, "42501", "permission denied for table am_acl", ""))
	})
}
