package reference_test

import (
	"bytes"
	"context"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUpdateRowControls(t *testing.T) {
	updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		updateRowSeed(t, p, b)
		retained, err := b.DB.QueryContext(p.Ctx, "SELECT id,a,b FROM t ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		stmt, err := b.DB.PrepareContext(p.Ctx, "UPDATE t SET (a,b)=ROW($1,b+$2) WHERE id=2 RETURNING a,b")
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec("UPDATE t SET (a,b)=(b,a)", 2))
		live := [][]any{{int64(1), int64(20), int64(10)}, {int64(2), int64(40), int64(30)}}
		run(updateRowSnapshot(live))
		if !b.PG {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(dump.String(), "UPDATE") {
				t.Fatal("dump contains assignment history", dump.String())
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader("CREATE TABLE broken(v INT); UPDATE broken SET (v)=ROW(1);")); err == nil {
				t.Fatal("Restore admitted DML history")
			}
			run(updateRowSnapshot(live))
			var afterFailure bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &afterFailure); err != nil || afterFailure.String() != dump.String() {
				t.Fatal("failed Restore changed live catalog", err, afterFailure.String())
			}
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(canceled, p.Native, bytes.NewReader(dump.Bytes())); err == nil {
				t.Fatal("canceled Restore succeeded")
			}
			run(updateRowSnapshot(live))
			var afterCancel bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &afterCancel); err != nil || afterCancel.String() != dump.String() {
				t.Fatal("canceled Restore changed live catalog", err, afterCancel.String())
			}
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
			run(updateRowSnapshot(live))
			var again bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || again.String() != dump.String() {
				t.Fatal("canonical live rows", err, again.String())
			}
		}
		fixture.UpdateCoreRetained(t, b.PG, retained, []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(30), int64(40)}}, false)
		for _, tc := range []struct {
			a     any
			delta int
			want  [][]any
		}{
			{9, 1, [][]any{{int64(9), int64(31)}}},
			{nil, 2, [][]any{{nil, int64(33)}}},
		} {
			rows, err := stmt.QueryContext(p.Ctx, tc.a, tc.delta)
			if err != nil {
				t.Fatal(err)
			}
			fixture.UpdateCoreRetained(t, b.PG, rows, []string{"a", "b"}, []string{"INT4", "INT4"}, tc.want, false)
		}
	})
}

func TestReferenceUpdateRowStored(t *testing.T) {
	t.Run("writable_view", func(t *testing.T) {
		updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			updateRowSeed(t, p, b)
			run(fixture.Exec("CREATE VIEW v AS SELECT id,b AS x,a AS y,a+b AS computed FROM t", 0))
			run(fixture.Exec("UPDATE v SET (x,y)=(SELECT y+1,x+2) WHERE id=1", 1))
			run(updateRowSnapshot([][]any{{int64(1), int64(22), int64(11)}, {int64(2), int64(30), int64(40)}}))
			q := "UPDATE v SET (x,computed)=ROW(1,2)"
			run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: `cannot update column "computed" of view "v"`, Detail: "View columns that are not columns of their base relation are not updatable."}))
			run(updateRowSnapshot([][]any{{int64(1), int64(22), int64(11)}, {int64(2), int64(30), int64(40)}}))
		})
	})
	t.Run("stored_grouped_action", func(t *testing.T) {
		updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			updateRowSeed(t, p, b)
			run(fixture.Exec("CREATE TABLE trigger_input(id INT)", 0))
			run(fixture.Exec("CREATE SEQUENCE draw", 0))
			run(fixture.Exec("CREATE RULE grouped_action AS ON INSERT TO trigger_input DO ALSO UPDATE t SET (a,b)=(WITH v AS MATERIALIZED (SELECT nextval('draw')+t.id*0 AS x) SELECT x,x+100 FROM v) WHERE t.id=NEW.id", 0))
			run(fixture.Exec("INSERT INTO trigger_input VALUES(1)", 1))
			run(updateRowSnapshot([][]any{{int64(1), int64(1), int64(101)}, {int64(2), int64(30), int64(40)}}))
			if !b.PG {
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String()+"CREATE TABLE broken(v missing_type);")); err == nil {
					t.Fatal("invalid catalog replay succeeded")
				}
				run(updateRowSnapshot([][]any{{int64(1), int64(1), int64(101)}, {int64(2), int64(30), int64(40)}}))
				var afterFailure bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &afterFailure); err != nil || afterFailure.String() != dump.String() {
					t.Fatal("failed Restore changed stored rule catalog", err, afterFailure.String())
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				var again bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil || dump.String() != again.String() {
					t.Fatal("canonical grouped rule replay", err, again.String())
				}
			}
			// The same held consumer survives subsequent target rename and
			// native catalog replacement, importing current rule/source ownership.
			held, err := b.DB.PrepareContext(p.Ctx, "INSERT INTO trigger_input VALUES($1)")
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			result, err := held.ExecContext(p.Ctx, 2)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 1)
			run(updateRowSnapshot([][]any{{int64(1), int64(1), int64(101)}, {int64(2), int64(2), int64(102)}}))
			run(fixture.Select("SELECT last_value FROM draw", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
			run(fixture.Exec("ALTER TABLE t RENAME COLUMN id TO renamed", 0))
			if !b.PG {
				var renamed bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &renamed); err != nil {
					t.Fatal("renamed grouped rule render", err)
				}
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(renamed.Bytes())); err != nil {
					t.Fatal("renamed grouped rule rebind", err)
				}
				var canonical bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &canonical); err != nil || canonical.String() != renamed.String() {
					t.Fatal("renamed grouped rule canonical replay", err, canonical.String())
				}
			}
			result, err = held.ExecContext(p.Ctx, 1)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 1)
			run(fixture.Select("SELECT renamed,a,b FROM t ORDER BY renamed", []string{"renamed", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(3), int64(103)}, {int64(2), int64(2), int64(102)}}))
			run(fixture.Select("SELECT last_value FROM draw", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(3)}}))
		})
	})
	t.Run("new_consumers", func(t *testing.T) {
		updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			updateRowSeed(t, p, b)
			run(fixture.Exec("CREATE TABLE log(id INT,a INT,b INT,again INT)", 0))
			run(fixture.Exec("CREATE SEQUENCE draw", 0))
			run(fixture.Exec("CREATE RULE consume_new AS ON UPDATE TO t DO ALSO INSERT INTO log VALUES(NEW.id,NEW.a,NEW.b,NEW.a)", 0))
			q := "UPDATE t SET (a,b)=(WITH v AS MATERIALIZED (SELECT nextval('draw')+t.id*0 AS x) SELECT x,x+100 FROM v)"
			want := fixture.Diagnostic{Code: "0A000", Message: "NEW variables in ON UPDATE rules cannot reference columns that are part of a multiple assignment in the subject UPDATE command"}
			// Pinned18.6 confirmed the complete diagnostic, including absent
			// optional fields/positions, on each direct and prepared route below.
			for _, prefix := range []string{"", "/*é*/ "} {
				for _, route := range []string{"exec", "query", "prepare"} {
					t.Run(route+"/"+prefix, func(t *testing.T) {
						s := fixture.ImplicitError(prefix+q, want)
						s.SelectRows = route == "query"
						if route == "prepare" {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						}
						fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
					})
				}
			}
			run(updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(30), int64(40)}}))
			run(fixture.Select("SELECT id,a,b,again FROM log", []string{"id", "a", "b", "again"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{}))
			run(fixture.Select("SELECT last_value,is_called FROM draw", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), false}}))
		})
	})
	t.Run("constructor_new_consumers", func(t *testing.T) {
		updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			updateRowSeed(t, p, b)
			run(fixture.Exec("CREATE TABLE log(id INT,a INT,b INT,again INT)", 0))
			run(fixture.Exec("CREATE RULE consume_new AS ON UPDATE TO t DO ALSO INSERT INTO log VALUES(NEW.id,NEW.a,NEW.b,NEW.a)", 0))
			stmt, err := b.DB.PrepareContext(p.Ctx, "UPDATE t SET (a,b)=ROW(b,a)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			result, err := stmt.ExecContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 2)
			run(updateRowSnapshot([][]any{{int64(1), int64(20), int64(10)}, {int64(2), int64(40), int64(30)}}))
			run(fixture.Select("SELECT id,a,b,again FROM log ORDER BY id", []string{"id", "a", "b", "again"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(20), int64(10), int64(20)}, {int64(2), int64(40), int64(30), int64(40)}}))
			run(fixture.Exec("DELETE FROM log", 2))
			result, err = stmt.ExecContext(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LockAffected(t, result, 2)
			run(updateRowSnapshot([][]any{{int64(1), int64(10), int64(20)}, {int64(2), int64(30), int64(40)}}))
			run(fixture.Select("SELECT id,a,b,again FROM log ORDER BY id", []string{"id", "a", "b", "again"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(20), int64(10)}, {int64(2), int64(30), int64(40), int64(30)}}))
		})
	})
	t.Run("unaffected_new_old_consumers", func(t *testing.T) {
		updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			updateRowSeed(t, p, b)
			run(fixture.Exec("CREATE TABLE log(id INT,a INT,b INT,again INT)", 0))
			run(fixture.Exec("CREATE SEQUENCE draw", 0))
			run(fixture.Exec("CREATE RULE consume_old AS ON UPDATE TO t DO ALSO INSERT INTO log VALUES(NEW.id,OLD.a,OLD.b,OLD.a)", 0))
			run(fixture.Prepared(fixture.Exec("UPDATE t SET (a,b)=(WITH v AS MATERIALIZED (SELECT nextval('draw')+t.id*0 AS x) SELECT x,x+100 FROM v)", 2)))
			run(fixture.Select("SELECT id,a,b,again FROM log ORDER BY id", []string{"id", "a", "b", "again"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(10), int64(20), int64(10)}, {int64(2), int64(30), int64(40), int64(30)}}))
			run(fixture.Select("SELECT a,b FROM t ORDER BY a", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(101)}, {int64(2), int64(102)}}))
			run(fixture.Select("SELECT last_value FROM draw", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}}))
		})
	})
	t.Run("enclosing_cte", func(t *testing.T) {
		updateRowBackends(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			updateRowSeed(t, p, b)
			q := "WITH source AS MATERIALIZED (SELECT id,a+1 AS x,b+2 AS y FROM t), changed AS (UPDATE t SET (a,b)=(SELECT x,y FROM source WHERE source.id=t.id) RETURNING id,a,b) SELECT l.id,l.a,r.b FROM changed l JOIN changed r ON l.id=r.id ORDER BY l.id"
			run(fixture.Prepared(fixture.Select(q, []string{"id", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(11), int64(22)}, {int64(2), int64(31), int64(42)}})))
			run(updateRowSnapshot([][]any{{int64(1), int64(11), int64(22)}, {int64(2), int64(31), int64(42)}}))
		})
	})
}
