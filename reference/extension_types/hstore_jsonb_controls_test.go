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

func hstoreJSONBControlWant() [][]any {
	return [][]any{{`{"a": "012345"}`, `{"a": "012345"}`}, {`{"a": "2.345e+4"}`, `{"a": 23450}`}, {`{"a": "t"}`, `{"a": true}`}, {`{"a": null}`, `{"a": null}`}, {`{"a": "NULL"}`, `{"a": "NULL"}`}, {`{}`, `{}`}, {nil, nil}, {`{"a": "1"}`, `{"a": 1}`}}
}

func hstoreJSONBControlSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreInstall(t, p, b, c)
	for _, q := range []string{
		"CREATE TABLE hs_jsonb_controls(id integer,v hstore)",
		"CREATE VIEW hs_jsonb_view AS SELECT id,hstore_to_jsonb(v) AS strict_json,hstore_to_jsonb_loose(v) AS loose_json,v::jsonb AS cast_json FROM hs_jsonb_controls",
		"CREATE TABLE hs_jsonb_defaults(id integer,strict_json jsonb DEFAULT hstore_to_jsonb('a=>1.00'::hstore),loose_json jsonb DEFAULT hstore_to_jsonb_loose('a=>t'::hstore),cast_json jsonb DEFAULT ('a=>1.00'::hstore)::jsonb)",
		"CREATE TABLE hs_jsonb_saved(id integer,strict_json jsonb,loose_json jsonb)",
	} {
		hstoreRun(t, p, b, c, fixture.Exec(q, 0))
	}
	hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_jsonb_controls VALUES (1,'a=>012345'),(2,'a=>2.345e+4'),(3,'a=>t'),(4,'a=>NULL'),(5,'a=>\"NULL\"'),(6,''),(7,NULL),(8,'a=>1')", 8))
	hstoreRun(t, p, b, c, fixture.Exec("INSERT INTO hs_jsonb_saved VALUES (1,hstore_to_jsonb('a=>1.00'),hstore_to_jsonb_loose('a=>1.00'))", 1))
}

func hstoreJSONBControlVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	for _, q := range []string{hstoreJSONBQuery("v") + " FROM hs_jsonb_controls ORDER BY id", "SELECT strict_json,loose_json FROM hs_jsonb_view ORDER BY id"} {
		s := hstoreJSONBPair(b, q, hstoreJSONBControlWant())
		// View outputs are nullable stored columns, unlike computed projections.
		if strings.Contains(q, "FROM hs_jsonb_view") {
			s = hstoreJSONBSelect(b, q, []string{"strict_json", "loose_json"}, hstoreJSONBControlWant(), true)
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
	hstoreRun(t, p, b, db, hstoreJSONBSelect(b, "SELECT strict_json,loose_json FROM hs_jsonb_saved ORDER BY id", []string{"strict_json", "loose_json"}, [][]any{{`{"a": "1.00"}`, `{"a": 1.00}`}}, true))
	hstoreRun(t, p, b, db, fixture.Exec("INSERT INTO hs_jsonb_defaults(id) VALUES (1)", 1))
	hstoreRun(t, p, b, db, hstoreJSONBSelect(b, "SELECT strict_json,loose_json FROM hs_jsonb_defaults ORDER BY id", []string{"strict_json", "loose_json"}, [][]any{{`{"a": "1.00"}`, `{"a": true}`}}, true))
	hstoreRun(t, p, b, db, hstoreJSONBSelect(b, "SELECT cast_json FROM hs_jsonb_defaults ORDER BY id", []string{"cast_json"}, [][]any{{`{"a": "1.00"}`}}, true))
	castWant := make([][]any, len(hstoreJSONBControlWant()))
	for i, pair := range hstoreJSONBControlWant() {
		castWant[i] = []any{pair[0]}
	}
	hstoreRun(t, p, b, db, hstoreJSONBSelect(b, "SELECT cast_json FROM hs_jsonb_view ORDER BY id", []string{"cast_json"}, castWant, true))
	hstoreJSONBCatalogCheck(t, p, b, db)
	hstoreRun(t, p, b, db, fixture.Exec("DELETE FROM hs_jsonb_defaults", 1))
}

func hstoreJSONBReplay(t *testing.T, p *fixture.Pair, dump []byte) {
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
	hstoreJSONBControlVerify(t, p, pg, c)
}

func hstoreJSONBControlPhase(t *testing.T, phase string) {
	// Supplemental INSERT cells retain Restore's scalar whitelist. A stored
	// DEFAULT is a genuine admitted schema recipe, evaluated by the later
	// literal-only INSERT after earlier catalog and row effects were staged.
	overflowSQL := `
CREATE TABLE hs_jsonb_overflow_stage(id integer,j jsonb DEFAULT hstore_to_jsonb_loose('a=>1e131072'::hstore));
INSERT INTO hs_jsonb_saved VALUES (89,'{}','{}');
INSERT INTO hs_jsonb_overflow_stage(id) VALUES (90);
`
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreJSONBControlSeed(t, p, b, c)
		hstoreJSONBControlVerify(t, p, b, c)
		overflowAbsent := fixture.Select("SELECT relname FROM pg_class WHERE relname='hs_jsonb_overflow_stage' AND relnamespace='public'::regnamespace", []string{"relname"}, []string{"NAME"}, [][]any{})
		if b.PG {
			if phase == "overflow" {
				want := fixture.ControlsReferenceDiagnostic("22003", "value overflows numeric format")
				// PostgreSQL executes this complete statement stream in one
				// implicit transaction; the private native Restore stage must
				// expose the same error and preserve its incumbent instead.
				hstoreError(t, p, b, c, overflowSQL, false, false, false, want)
				hstoreJSONBControlVerify(t, p, b, c)
				hstoreRun(t, p, b, c, overflowAbsent)
			}
			return
		}
		dump := hstoreDump(t, p, p.Native)
		q := hstoreJSONBQuery("v") + " FROM hs_jsonb_controls ORDER BY id"
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
			hstoreRun(t, p, target, destination.Memory, fixture.Exec("CREATE TABLE hs_jsonb_displace(v integer)", 0))
			hstoreRun(t, p, target, destination.Memory, fixture.Exec("CREATE EXTENSION hstore", 0))
			if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump)); err != nil {
				t.Fatal(err)
			}
			hstoreJSONBControlVerify(t, p, target, destination.Memory)
		case "failed":
			script, want := hstoreStagedSQL()
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &want)
		case "jsonb_signature", "jsonb_loose_kind", "jsonb_duplicate", "jsonb_missing", "jsonb_cast_owner":
			err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(hstoreInvalidOwnerDump(t, dump, phase)))
			var state interface{ SQLState() string }
			if err == nil || !strings.Contains(err.Error(), "invalid extension owner metadata") || errors.As(err, &state) {
				t.Fatalf("owner corruption error %v", err)
			}
		case "canceled":
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump)); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled restore %v", err)
			}
		case "overflow":
			script := string(dump) + overflowSQL
			want := fixture.ControlsReferenceDiagnostic("22003", "value overflows numeric format")
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &want)
			hstoreRun(t, p, b, c, overflowAbsent)
		default:
			t.Fatal("unknown phase", phase)
		}
		hstoreJSONBControlVerify(t, p, b, c)
		hstoreRun(t, p, b, held, hstoreJSONBPair(b, q, hstoreJSONBControlWant()))
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
		if !reflect.DeepEqual(got, hstoreJSONBControlWant()) {
			t.Fatalf("retained JSON %#v", got)
		}
		for _, column := range columns {
			if column.DatabaseTypeName() != "JSONB" {
				t.Fatal("retained JSON metadata", column.DatabaseTypeName())
			}
		}
		if after := hstoreDump(t, p, p.Native); !bytes.Equal(dump, after) {
			t.Fatalf("replacement changed deterministic dump\nbefore=%s\nafter=%s", dump, after)
		}
		if phase == "same" {
			hstoreJSONBReplay(t, p, dump)
		}
	})
}

func TestReferenceHstoreJSONBControls(t *testing.T) {
	for _, phase := range []string{"same", "fresh", "failed", "canceled", "overflow", "jsonb_signature", "jsonb_loose_kind", "jsonb_duplicate", "jsonb_missing", "jsonb_cast_owner"} {
		t.Run(phase, func(t *testing.T) { hstoreJSONBControlPhase(t, phase) })
	}
}
