package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func bitOperatorsStoredStep(pg bool, id int) fixture.Step {
	names := []string{"cat", "a", "o", "x", "n", "l", "r", "eq", "ne", "lt", "le", "gt", "ge"}
	types := []string{"VARBIT", "BIT", "BIT", "BIT", "BIT", "BIT", "BIT", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}
	q := "SELECT cat,a,o,x,n,l,r,eq,ne,lt,le,gt,ge FROM bop_view WHERE id=$1"
	s := fixture.Select(q, names, types, [][]any{{"10001011", "10000000", "10001111", "01111011", "01110100", "00101100", "00100010", false, true, false, false, true, true}}, id)
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		cols, err := r.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		bitOperatorsMetadata(t, cols, names, types, !pg)
	}
	return s
}

func bitOperatorsStoredTable(id int) fixture.Step {
	return fixture.Select("SELECT v,n,l,r,g FROM bop_symbol WHERE id=$1", []string{"v", "n", "l", "r", "g"}, []string{"VARBIT", "BIT", "BIT", "BIT", "BIT"}, [][]any{{"10001011", "01110", "01000", "00100", "01110100"}}, id)
}

func TestReferenceBitOperatorsStoredReplay(t *testing.T) {
	bitOperatorsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE bop_symbol(id INTEGER,v VARBIT DEFAULT B'10001'||B'011',n "bit" DEFAULT ~B'10001',l "bit" DEFAULT B'10001'<<3,r "bit" DEFAULT B'10001'>>2,g "bit" GENERATED ALWAYS AS (~v) STORED,CONSTRAINT bop_valid CHECK(g<>v))`, 0))
		run(fixture.Exec("INSERT INTO bop_symbol(id) VALUES(1)", 1))
		run(fixture.Exec("CREATE VIEW bop_view AS SELECT id,v||B'' AS cat,v&B'11110000' AS a,v OPERATOR(pg_catalog.|) B'00001111' AS o,v#B'11110000' AS x,~v AS n,v<<2 AS l,v>>2 AS r,g=v AS eq,g<>v AS ne,v<B'1' AS lt,v<=B'1' AS le,v>B'1' AS gt,v>=B'1' AS ge FROM bop_symbol", 0))
		bitOperatorsBoth(t, p, b, c, bitOperatorsStoredStep(b.PG, 1))
		bitOperatorsBoth(t, p, b, c, bitOperatorsStoredTable(1))
		if b.PG {
			// PostgreSQL exercises the same stored SQL/default demand, not a fake
			// host-control API. Actual emitted native replay is below, after cutover.
			run(fixture.Exec("INSERT INTO bop_symbol(id) VALUES(2)", 1))
			bitOperatorsBoth(t, p, b, c, bitOperatorsStoredStep(b.PG, 2))
			bitOperatorsBoth(t, p, b, c, bitOperatorsStoredTable(2))
			return
		}
		q := bitOperatorsStoredStep(b.PG, 1).Query
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		retained, err := stmt.QueryContext(p.Ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		cols, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bitOperatorsStoredStep(b.PG, 1))
		var redump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil || !bytes.Equal(dump.Bytes(), redump.Bytes()) {
			t.Fatal("unstable symbolic redump", err, redump.String())
		}
		run(fixture.Exec("INSERT INTO bop_symbol(id) VALUES(2)", 1))
		bitOperatorsBoth(t, p, b, c, bitOperatorsStoredStep(b.PG, 2))
		bitOperatorsBoth(t, p, b, c, bitOperatorsStoredTable(2))
		if !retained.Next() {
			t.Fatal(retained.Err())
		}
		want := bitOperatorsStoredStep(b.PG, 1)
		values, dst := make([]any, len(want.Columns)), make([]any, len(want.Columns))
		for i := range values {
			dst[i] = &values[i]
		}
		if err := retained.Scan(dst...); err != nil {
			t.Fatal(err)
		}
		for i, v := range values {
			fixture.AssertRawValue(t, false, want.Types[i], v)
		}
		if !reflect.DeepEqual(values, want.Rows[0]) {
			t.Fatal("replacement changed retained values", values)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("replacement changed retained cursor", retained.Err())
		}
		bitOperatorsMetadata(t, cols, want.Columns, want.Types, true)
		if err := retained.Close(); err != nil {
			t.Fatal(err)
		}
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(`CREATE TABLE bop_bad(v "bit"); INSERT INTO bop_bad VALUES(B'10'&B'11'),(B'1'&B'00');`))
		if err == nil {
			t.Fatal("late-width Restore succeeded")
		}
		d := fixture.ControlsReferenceDiagnostic("22026", "cannot AND bit strings of different sizes")
		fixture.AssertDiagnostic(t, err, &d)
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil || !bytes.Equal(before.Bytes(), after.Bytes()) {
			t.Fatal("failed Restore changed catalog", err)
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bitOperatorsStoredStep(b.PG, 1))
		if p.Postgres != nil {
			// Consume the real emitted SQL on PostgreSQL before its ordinary shared
			// scenario is run. Leave public empty for that subsequent callback.
			var pg fixture.Backend
			for _, candidate := range p.Backends() {
				if candidate.PG {
					pg = candidate
				}
			}
			for _, sql := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public", dump.String()} {
				if _, err := pg.DB.ExecContext(p.Ctx, sql); err != nil {
					t.Fatal("actual native dump replay", err, sql)
				}
			}
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, bitOperatorsStoredStep(true, 1))
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, fixture.Exec("INSERT INTO bop_symbol(id) VALUES(2)", 1))
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, bitOperatorsStoredStep(true, 2))
			fixture.RunStep(t, p.Ctx, pg.DB, pg.Recorder, bitOperatorsStoredTable(2))
			for _, sql := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
				if _, err := pg.DB.ExecContext(p.Ctx, sql); err != nil {
					t.Fatal(err)
				}
			}
		}
	})
}
