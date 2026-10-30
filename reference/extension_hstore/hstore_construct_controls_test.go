package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func hstoreConstructSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	hstoreInstall(t, p, b, c)
	hstoreRun(t, p, b, c, fixture.Exec("CREATE TABLE hs_construct_smoke(id integer,v hstore)", 0))
	hstoreRun(t, p, b, c, fixture.Exec(`INSERT INTO hs_construct_smoke VALUES (1,hstore('a',NULL::text)),(2,hstore(ARRAY['a','1','b','2'])),(3,hstore(ARRAY['a','b'],ARRAY['1',NULL])),(4,hstore($$a"b\c$$,$$x"y\z$$))`, 4))
	hstoreRun(t, p, b, c, fixture.Exec("CREATE VIEW hs_construct_view AS SELECT id,hstore('a',v->'a')::text AS made,fetchval(v,'a') AS fetched,v->'a' AS scalar,(v->ARRAY['a','missing'])::text AS requested,exist(v,'a') AS present,defined(v,'a') AS valued FROM hs_construct_smoke WHERE id<=3", 0))
}

func hstoreConstructVerify(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor) {
	t.Helper()
	q := "SELECT id,v::text AS v,exist(v,'a') AS e,defined(v,'a') AS d,v->'a' AS f,(v->ARRAY['a','missing'])::text AS a FROM hs_construct_smoke WHERE id<=3 ORDER BY id"
	want := [][]any{{int64(1), `"a"=>NULL`, true, false, nil, "{NULL,NULL}"}, {int64(2), `"a"=>"1", "b"=>"2"`, true, true, "1", "{1,NULL}"}, {int64(3), `"a"=>"1", "b"=>NULL`, true, true, "1", "{1,NULL}"}}
	s := fixture.Select(q, []string{"id", "v", "e", "d", "f", "a"}, []string{"INT4", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT"}, want)
	hstoreRun(t, p, b, db, s)
	hstoreRun(t, p, b, db, fixture.Prepared(s))
	hstoreRun(t, p, b, db, fixture.Select("SELECT id,made,fetched,scalar,requested,present,valued FROM hs_construct_view ORDER BY id", []string{"id", "made", "fetched", "scalar", "requested", "present", "valued"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, [][]any{{int64(1), `"a"=>NULL`, nil, nil, "{NULL,NULL}", true, false}, {int64(2), `"a"=>"1"`, "1", "1", "{1,NULL}", true, true}, {int64(3), `"a"=>"1"`, "1", "1", "{1,NULL}", true, true}}))
	hstoreRun(t, p, b, db, fixture.Select(`SELECT v->$$a"b\c$$ AS scalar,fetchval(v,$$a"b\c$$) AS named FROM hs_construct_smoke WHERE id=4`, []string{"scalar", "named"}, []string{"TEXT", "TEXT"}, [][]any{{`x"y\z`, `x"y\z`}}))
	hstoreRun(t, p, b, db, fixture.ArrayReferenceSelect("SELECT v->ARRAY['a','missing'] AS requested FROM hs_construct_smoke WHERE id<=3 ORDER BY id", []string{"requested"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{"{NULL,NULL}"}, {"{1,NULL}"}, {"{1,NULL}"}}, b.PG))
	hstoreRun(t, p, b, db, hstoreSelect(b, "SELECT hstore('a',v->'a') AS constructed FROM hs_construct_smoke WHERE id<=3 ORDER BY id", []string{"constructed"}, [][]any{{`"a"=>NULL`}, {`"a"=>"1"`}, {`"a"=>"1"`}}, nil))
}

func TestReferenceHstoreConstructFeatureSmoke(t *testing.T) {
	hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		hstoreConstructSeed(t, p, b, c)
		hstoreConstructVerify(t, p, b, c)
		q := "SELECT hstore($1::text,$2::text)->$1::text AS v"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, value := range []any{"1", nil} {
			hstoreRun(t, p, b, held, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{value}}, "a", value))
		}
		hstoreRun(t, p, b, c, hstoreSelect(b, "SELECT hstore(ARRAY[['a','1']]) AS v", []string{"v"}, [][]any{{`"a"=>"1"`}}, nil))
		hstoreRun(t, p, b, c, fixture.ArrayReferenceSelect("SELECT 'a=>1'::hstore->'[0:1]={a,missing}'::text[] AS a", []string{"a"}, []string{"_TEXT"}, []string{"_TEXT"}, [][]any{{"[0:1]={1,NULL}"}}, b.PG))
		hstoreError(t, p, b, c, "SELECT hstore(ARRAY['a','b'],ARRAY['1'])", false, true, false, fixture.ControlsReferenceDiagnostic("2202E", "arrays must have same bounds"))
		hstoreRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM hs_construct_smoke", []string{"n"}, []string{"INT8"}, [][]any{{int64(4)}}))
	})
	// Separate bounded scenarios retain actual stored calls through both controls.
	t.Run("controls", TestReferenceHstoreConstructControls)
}

func TestReferenceHstoreConstructControls(t *testing.T) {
	phases := []string{"same", "fresh"}
	for slot := 31; slot <= 39; slot++ {
		phases = append(phases, fmt.Sprintf("signature_%d", slot), fmt.Sprintf("kind_%d", slot))
	}
	for _, phase := range phases {
		t.Run(phase, func(t *testing.T) {
			hstoreScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				hstoreConstructSeed(t, p, b, c)
				hstoreConstructVerify(t, p, b, c)
				if b.PG {
					return
				} // Public native controls have no SQL equivalent.
				dump := hstoreDump(t, p, p.Native)
				retained, err := b.DB.QueryContext(p.Ctx, "SELECT hstore('a',v->'a') AS v FROM hs_construct_smoke WHERE id<=3 ORDER BY id")
				if err != nil {
					t.Fatal(err)
				}
				defer retained.Close()
				columns, err := retained.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				hstoreColumns(t, false, columns, []string{"v"}, nil)
				q := "SELECT hstore('a',v->'a') AS v FROM hs_construct_smoke WHERE id=2"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				hstoreRun(t, p, b, held, hstoreSelect(b, q, []string{"v"}, [][]any{{`"a"=>"1"`}}, nil))
				textQuery := "SELECT hstore($1::text,$2::text)->$1::text AS v"
				textStmt, err := c.PrepareContext(p.Ctx, textQuery)
				if err != nil {
					t.Fatal(err)
				}
				defer textStmt.Close()
				textHeld := fixture.HeldStatement{Executor: c, Statement: textStmt, SQL: textQuery}
				hstoreRun(t, p, b, textHeld, fixture.Select(textQuery, []string{"v"}, []string{"TEXT"}, [][]any{{"before"}}, "a", "before"))
				if phase == "same" {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
				} else if phase == "fresh" {
					destination := fixture.NewFixture(t, false, true)
					if _, err := destination.Native.ExecContext(p.Ctx, "CREATE TABLE hs_displace(v integer); CREATE EXTENSION hstore"); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, destination.Native, bytes.NewReader(dump)); err != nil {
						t.Fatal(err)
					}
					target := fixture.Backend{DB: destination.Memory, Recorder: destination.MemoryRecord}
					hstoreConstructVerify(t, p, target, destination.Memory)
				} else {
					err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(hstoreInvalidOwnerDump(t, dump, phase)))
					var state interface{ SQLState() string }
					if err == nil || !strings.Contains(err.Error(), "invalid extension owner metadata") || errors.As(err, &state) {
						t.Fatalf("ordinary owner error: %v", err)
					}
					if after := hstoreDump(t, p, p.Native); !bytes.Equal(after, dump) {
						t.Fatal("failed Restore changed deterministic dump")
					}
				}
				hstoreConstructVerify(t, p, b, c)
				if phase == "same" {
					// Reset retires the installed result identity, not detached old rows.
					stale := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type"))
					stale.SelectRows, stale.ErrorStage = true, "execute"
					hstoreRun(t, p, b, held, stale)
				} else {
					hstoreRun(t, p, b, held, hstoreSelect(b, q, []string{"v"}, [][]any{{`"a"=>"1"`}}, nil))
				}
				// A builtin-only public signature can rebind its installed constructor.
				for _, value := range []any{"after", nil} {
					hstoreRun(t, p, b, textHeld, fixture.Select(textQuery, []string{"v"}, []string{"TEXT"}, [][]any{{value}}, "a", value))
				}
				var got []any
				for retained.Next() {
					var v any
					if err := retained.Scan(&v); err != nil {
						t.Fatal(err)
					}
					got = append(got, v)
				}
				if err := retained.Err(); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, []any{`"a"=>NULL`, `"a"=>"1"`, `"a"=>"1"`}) {
					t.Fatalf("retained values %#v", got)
				}
				hstoreColumns(t, false, columns, []string{"v"}, nil)
				if phase == "same" && p.Postgres != nil {
					pg := fixture.Backend{PG: true, DB: p.Postgres, Recorder: p.PostgresRecord}
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					if _, err := p.Postgres.ExecContext(p.Ctx, string(dump)); err != nil {
						t.Fatal(err)
					}
					hstoreConstructVerify(t, p, pg, p.Postgres)
					if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
				}
			})
		})
	}
}
