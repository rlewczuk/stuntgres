package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRangeInspectBinds(t *testing.T) {
	inspectScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		for _, f := range inspectFamilies {
			names := []string{f.name}
			if f.name == "int4range" || f.name == "int8range" || f.name == "numrange" {
				names = append(names, strings.Replace(f.name, "range", "multirange", 1))
			}
			for _, name := range names {
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inspect_bind(v "+name+")", 0))
				insertSQL := "INSERT INTO inspect_bind VALUES ($1)"
				insert, err := b.DB.PrepareContext(p.Ctx, insertSQL)
				if err != nil {
					t.Fatal(err)
				}
				insertHeld := fixture.HeldStatement{Executor: b.DB, Statement: insert, SQL: insertSQL}
				q := inspectTupleQuery("v") + " FROM inspect_bind"
				query, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				queryHeld := fixture.HeldStatement{Executor: b.DB, Statement: query, SQL: q}
				finite, empty, unbounded := f.finite, "empty", "(,)"
				if strings.Contains(name, "multirange") {
					finite = "{" + finite + "}"
					empty = "{}"
					unbounded = "{(,)}"
				}
				li, ui := true, false
				if f.name == "numrange" {
					li, ui = false, true
				}
				if f.name == "tsrange" || f.name == "tstzrange" {
					ui = true
				}
				owned := []byte(finite)
				for _, tc := range []struct {
					input any
					row   []any
				}{
					{finite, []any{inspectBound(b, f.subtype, f.lo), inspectBound(b, f.subtype, f.hi), false, li, ui, false, false}},
					{owned, []any{inspectBound(b, f.subtype, f.lo), inspectBound(b, f.subtype, f.hi), false, li, ui, false, false}},
					{empty, []any{nil, nil, true, false, false, false, false}},
					{unbounded, []any{nil, nil, false, false, false, true, true}},
					{nil, []any{nil, nil, nil, nil, nil, nil, nil}}, {[]byte(nil), []any{nil, nil, nil, nil, nil, nil, nil}},
				} {
					fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, fixture.Exec(insertSQL, 1, tc.input))
					s := inspectTupleStep(b, "v", f.subtype, tc.row)
					s.Query = q
					fixture.RunStep(t, p.Ctx, queryHeld, b.Recorder, s)
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DELETE FROM inspect_bind", 1))
				}
				fixture.RunStep(t, p.Ctx, insertHeld, b.Recorder, fixture.Exec(insertSQL, 1, owned))
				s := inspectTupleStep(b, "v", f.subtype, []any{inspectBound(b, f.subtype, f.lo), inspectBound(b, f.subtype, f.hi), false, li, ui, false, false})
				s.Query = q
				rows, err := query.QueryContext(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				s.CheckMetadata(t, rows)
				for i := range owned {
					owned[i] = 'x'
				}
				rangeConsumeHeld(t, rows, s)
				fixture.RunStep(t, p.Ctx, queryHeld, b.Recorder, s)
				failure := errors.New("inspection Valuer boundary")
				for _, execute := range []func() (sql.Result, error){
					func() (sql.Result, error) {
						return b.DB.ExecContext(p.Ctx, insertSQL, integerRangeFailingValuer{failure})
					},
					func() (sql.Result, error) { return insert.ExecContext(p.Ctx, integerRangeFailingValuer{failure}) },
				} {
					result, err := execute()
					var state interface{ SQLState() string }
					if result != nil || !errors.Is(err, failure) || errors.As(err, &state) {
						t.Fatal("Valuer crossed SQL boundary", result, err)
					}
				}
				if err := query.Close(); err != nil {
					t.Fatal(err)
				}
				if err := insert.Close(); err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE inspect_bind", 0))
			}
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE r(v int4range)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO r VALUES ('[1,2)')", 1))
		q := "SELECT range_merge(v,$1) AS hull FROM r"
		stmt, err := b.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
		for _, tc := range []struct{ input, w any }{{"[3,4)", "[1,4)"}, {"empty", "[1,2)"}, {nil, nil}, {[]byte(nil), nil}} {
			s := inspectHullStep(b, "range_merge(v,$1)", "int4range", tc.w)
			s.Query = q
			s.Args = []any{tc.input}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
	})
}

func inspectCanceled(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE inspect_cancel(v int8range)", 0))
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO inspect_cancel VALUES ('[1,4)')", 1))
	q := "SELECT lower(v) AS lo,range_merge(v,$1) AS hull FROM inspect_cancel"
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	for _, query := range []func() (*sql.Rows, error){
		func() (*sql.Rows, error) { return b.DB.QueryContext(ctx, q, "empty") },
		func() (*sql.Rows, error) { return stmt.QueryContext(ctx, "empty") },
	} {
		rows, err := query()
		var state interface{ SQLState() string }
		if rows != nil || !errors.Is(err, context.Canceled) || errors.As(err, &state) {
			t.Fatal("cancellation crossed SQL boundary", rows, err)
		}
	}
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("DROP TABLE inspect_cancel", 0))
}
func TestReferenceRangeInspectCancellation(t *testing.T) {
	inspectScenario(t, func(p *fixture.Pair, b fixture.Backend) { inspectCanceled(t, p, b) })
}
