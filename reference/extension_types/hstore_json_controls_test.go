package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func hstoreJSONControlWant() [][]any {
	return [][]any{{`{"a": "012345"}`, `{"a": "012345"}`}, {`{"a": "1.00"}`, `{"a": 1.00}`}, {`{"a": "t"}`, `{"a": true}`}, {`{"a": null}`, `{"a": null}`}, {`{"a": "NULL"}`, `{"a": "NULL"}`}, {`{}`, `{}`}, {nil, nil}}
}

func hstoreJSONControlSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreInstall(t, p, b, c)
	for _, q := range []string{
		"CREATE TABLE hs_json_controls(id integer,v hstore)",
		"CREATE VIEW hs_json_view AS SELECT id,hstore_to_json(v) AS strict_json,hstore_to_json_loose(v) AS loose_json FROM hs_json_controls",
		"CREATE TABLE hs_json_defaults(id integer,strict_json json DEFAULT hstore_to_json('a=>1.00'::hstore),loose_json json DEFAULT hstore_to_json_loose('a=>t'::hstore))",
		"CREATE TABLE hs_json_saved(id integer,strict_json json,loose_json json)",
	} {
		hstoreRun(t, p, b, c, fixture.Exec(q, 0))
	}
	hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_json_controls VALUES (1,'a=>012345'),(2,'a=>1.00'),(3,'a=>t'),(4,'a=>NULL'),(5,'a=>\"NULL\"'),(6,''),(7,NULL)", 7))
	hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_json_saved VALUES (1,hstore_to_json('a=>1.00'),hstore_to_json_loose('a=>1.00'))", 1))
}

func hstoreJSONControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	for _, q := range []string{hstoreJSONQuery("v") + " FROM hs_json_controls ORDER BY id", "SELECT strict_json,loose_json FROM hs_json_view ORDER BY id"} {
		s := hstoreJSONPair(b, q, hstoreJSONControlWant())
		// View outputs are nullable stored columns, unlike computed projections.
		if strings.Contains(q, "FROM hs_json_view") {
			s = hstoreJSONSelect(b, q, []string{"strict_json", "loose_json"}, hstoreJSONControlWant(), true)
		}
		hstoreRun(t, p, b, db, s)
		hstoreRun(t, p, b, db, fixture.Prepared(s))
		stmt, err := db.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		hstoreRun(t, p, b, fixture.HeldStatement{Executor: db, Statement: stmt, SQL: q}, s)
		stmt.Close()
	}
	hstoreRun(t, p, b, db, hstoreJSONSelect(b, "SELECT strict_json,loose_json FROM hs_json_saved ORDER BY id", []string{"strict_json", "loose_json"}, [][]any{{`{"a": "1.00"}`, `{"a": 1.00}`}}, true))
	hstoreRun(t, p, b, db, fixture.Exec("INSERT INTO hs_json_defaults(id) VALUES (1)", 1))
	hstoreRun(t, p, b, db, hstoreJSONSelect(b, "SELECT strict_json,loose_json FROM hs_json_defaults ORDER BY id", []string{"strict_json", "loose_json"}, [][]any{{`{"a": "1.00"}`, `{"a": true}`}}, true))
	hstoreRun(t, p, b, db, fixture.Exec("DELETE FROM hs_json_defaults", 1))
}

func hstoreJSONReplay(t *testing.T, p *fixture.Pair, dump []byte) {
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
	if _, err := c.ExecContext(p.Ctx, string(dump)); err != nil {
		t.Fatalf("actual emitted SQL replay: %v", err)
	}
	pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
	hstoreJSONControlVerify(t, p, pg, c)
}

func hstoreJSONControlPhase(t *testing.T, phase string) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreJSONControlSeed(t, p, b, c)
		hstoreJSONControlVerify(t, p, b, c)
		if b.PG {
			return
		}
		dump := hstoreDump(t, p, p.Native)
		q := hstoreJSONQuery("v") + " FROM hs_json_controls ORDER BY id"
		rows, err := b.DB.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		switch phase {
		case "same":
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
		case "fresh":
			destination := fixture.NewFixture(t, false, true)
			target := fixture.Backend{DB: destination.Memory, Recorder: destination.MemoryRecord}
			hstoreRun(t, p, target, destination.Memory, fixture.Exec("CREATE TABLE hs_json_displace(v integer)", 0))
			hstoreRun(t, p, target, destination.Memory, fixture.Exec("CREATE EXTENSION hstore", 0))
			if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			hstoreJSONControlVerify(t, p, target, destination.Memory)
		case "failed":
			script, want := hstoreStagedSQL()
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &want)
		case "json_signature", "json_loose_kind":
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(hstoreInvalidOwnerDump(t, dump, phase)))
			var state interface{ SQLState() string }
			if err == nil || !strings.Contains(err.Error(), "invalid extension owner metadata") || errors.As(err, &state) {
				t.Fatalf("owner corruption error %v", err)
			}
		default:
			t.Fatal("unknown phase", phase)
		}
		hstoreJSONControlVerify(t, p, b, c)
		hstoreRun(t, p, b, held, hstoreJSONPair(b, q, hstoreJSONControlWant()))
		var got [][]any
		for rows.Next() {
			var a, z any
			if err := rows.Scan(&a, &z); err != nil {
				t.Fatal(err)
			}
			got = append(got, []any{a, z})
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, hstoreJSONControlWant()) {
			t.Fatalf("retained JSON %#v", got)
		}
		for _, column := range columns {
			if column.DatabaseTypeName() != "JSON" {
				t.Fatal("retained JSON metadata", column.DatabaseTypeName())
			}
		}
		if after := hstoreDump(t, p, p.Native); !bytes.Equal(dump, after) {
			t.Fatalf("replacement changed deterministic dump\nbefore=%s\nafter=%s", dump, after)
		}
		if phase == "same" {
			hstoreJSONReplay(t, p, dump)
		}
	})
}

func TestReferenceHstoreJSONControls(t *testing.T) {
	for _, phase := range []string{"same", "fresh", "failed", "json_signature", "json_loose_kind"} {
		t.Run(phase, func(t *testing.T) { hstoreJSONControlPhase(t, phase) })
	}
}
