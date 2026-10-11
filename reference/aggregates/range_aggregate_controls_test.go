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

func TestReferenceRangeAggregateControls(t *testing.T) {
	var emitted string
	rangeAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		if b.PG && emitted != "" {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			if _, err = conn.ExecContext(p.Ctx, emitted); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, rangeAggregateStep(true, "SELECT u,i FROM ra_controls", []string{"u", "i"}, []string{"INT4MULTIRANGE", "INT4RANGE"}, [][]any{{"{[1,4),[7,9)}", "empty"}}))
			return
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(v INT4RANGE)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES('[1,4)'),('[7,9)')", 2))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW ra_controls AS SELECT range_agg(v) AS u,range_intersect_agg(v) AS i FROM r", 0))
		q := "SELECT u,i FROM ra_controls"
		s := rangeAggregateStep(b.PG, q, []string{"u", "i"}, []string{"INT4MULTIRANGE", "INT4RANGE"}, [][]any{{"{[1,4),[7,9)}", "empty"}})
		rangeAggregateRoutes(t, p, b, s)
		if b.PG {
			return
		} // PostgreSQL has no native control API.
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held, err := stmt.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		before, err := held.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		var dump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		emitted = dump.String()
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES('[3,8)')", 1))
		if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, q}, b.Recorder, s)
		if err = stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+"\nCREATE TABLE invalid(v missing_ra_type);")); err == nil {
			t.Fatal("invalid replacement succeeded")
		}
		var redump bytes.Buffer
		if err = stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
			t.Fatal(err)
		}
		if redump.String() != emitted {
			t.Fatal("failed replacement changed dump")
		}
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{b.DB, stmt, q}, b.Recorder, s)
		if !held.Next() {
			t.Fatal("held result missing", held.Err())
		}
		var u, i any
		if err = held.Scan(&u, &i); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual([]any{u, i}, []any{"{[1,4),[7,9)}", "empty"}) {
			t.Fatalf("held values %v/%v", u, i)
		}
		if held.Next() || held.Err() != nil {
			t.Fatal("held exhaustion", held.Err())
		}
		if before[0].DatabaseTypeName() != "INT4MULTIRANGE" || before[1].DatabaseTypeName() != "INT4RANGE" {
			t.Fatal("held metadata changed")
		}
		driver := stuntgres.NewDriver()
		connector, err := driver.OpenConnector("range-aggregate-fresh/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err = stuntgres.Restore(p.Ctx, fresh, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		rows, err := fresh.QueryContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if !rows.Next() {
			t.Fatal(rows.Err())
		}
		if err = rows.Scan(&u, &i); err != nil {
			t.Fatal(err)
		}
		if u != "{[1,4),[7,9)}" || i != "empty" {
			t.Fatal(u, i)
		}
		if rows.Next() || rows.Err() != nil {
			t.Fatal(rows.Err())
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if rows, err := b.DB.QueryContext(ctx, q); !errors.Is(err, context.Canceled) || rows != nil {
			if rows != nil {
				rows.Close()
			}
			t.Fatalf("canceled query %v/%v", rows, err)
		}
	})
}
