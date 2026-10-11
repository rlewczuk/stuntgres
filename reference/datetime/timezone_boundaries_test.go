package reference_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTimezoneRangeAndUpdateAtomicity(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	for _, input := range []string{"4714-11-24 00:00+01 BC", "294276-12-31 23:59:59.999999-01"} {
		q := "SELECT TIMESTAMPTZ '" + input + "' FROM tz_witness"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22008", Message: `timestamp out of range: "` + input + `"`, Position: strconv.Itoa(strings.Index(q, "'") + 1)})
		s.SelectRows = true
		s.ErrorStage = "execute"
		p.Step(t, s)
		s.ErrorStage = "prepare"
		p.Step(t, fixture.Prepared(s))
	}
	// Root's pinned18.0 oracle observed this typmod carry is accepted, even
	// though fresh input of the resulting year lies beyond the input boundary.
	timezoneBoth(t, p, timezoneTextStep("(TIMESTAMPTZ '294276-12-31 23:59:59.999999+00')::TIMESTAMPTZ(0)", "294277-01-01 00:00:00+00"))
	p.Step(t, fixture.Exec("UPDATE tz_witness SET z='epoch' WHERE id=1", 1))
	p.Step(t, fixture.Exec("INSERT INTO tz_witness(id,z) VALUES(2,'epoch')", 1))
	sentinel := fixture.Select("SELECT id,z::TEXT AS z FROM tz_witness ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1970-01-01 00:00:00+00"}, {int64(2), "1970-01-01 00:00:00+00"}})
	s := fixture.ImplicitError("UPDATE tz_witness SET z=(CASE WHEN id=1 THEN '2004-01-01+00' ELSE $1::TEXT END)::TIMESTAMPTZ", fixture.Diagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "nonsense"`}, "nonsense")
	s.ErrorStage = "execute"
	timezoneBoth(t, p, s)
	timezoneBoth(t, p, sentinel)
	q := "UPDATE tz_witness SET z='infinity'; nonsense"
	s = fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: `syntax error at or near "nonsense"`, Position: strconv.Itoa(strings.Index(q, "nonsense") + 1)})
	s.ErrorStage = "execute"
	p.Step(t, s)
	s.ErrorStage = "prepare"
	p.Step(t, fixture.Prepared(s))
	timezoneBoth(t, p, sentinel)
	for _, backend := range p.Backends() {
		// Context canceled after query creation must not invent an SQL diagnostic.
		// pq may already have buffered a complete wire result; only the native
		// lazy cursor promises cancellation before its next exported row.
		if backend.DB != p.Memory {
			continue
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		rows, err := backend.DB.QueryContext(ctx, "SELECT z FROM tz_witness ORDER BY id")
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		if rows.Next() {
			rows.Close()
			t.Fatal("canceled cursor produced a row")
		}
		if !errors.Is(rows.Err(), context.Canceled) {
			rows.Close()
			t.Fatal("iteration cancellation", rows.Err())
		}
		rows.Close()
		fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, sentinel)
	}
}

func TestReferenceTimezoneRelativeInputAndDeclarations(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	p.Step(t, fixture.Exec("CREATE TABLE tz_spelling(a TIMESTAMP(3) WITH TIME ZONE,b TIME(3) WITH TIME ZONE,c pg_catalog.timestamptz,d pg_catalog.timetz)", 0))
	p.Step(t, fixture.Prepared(fixture.Exec("CREATE TABLE tz_spelling_prepared(a TIMESTAMP(3) WITH TIME ZONE,b TIME(3) WITH TIME ZONE,c pg_catalog.timestamptz,d pg_catalog.timetz)", 0)))
	timezoneBoth(t, p, fixture.Select("SELECT a,b,c,d FROM tz_spelling", []string{"a", "b", "c", "d"}, []string{"TIMESTAMPTZ", "TIMETZ", "TIMESTAMPTZ", "TIMETZ"}, nil))
	// pg_attribute population is outside the baseline catalog contract. Assert
	// public metadata on actual NULL rows and typmod effects on assigned values.
	for _, table := range []string{"tz_spelling", "tz_spelling_prepared"} {
		insert := fixture.Exec("INSERT INTO "+table+" VALUES(NULL,NULL,NULL,NULL)", 1)
		insert.Prepared = table == "tz_spelling_prepared"
		p.Step(t, insert)
		timezoneBoth(t, p, fixture.Select("SELECT a,b,c,d FROM "+table, []string{"a", "b", "c", "d"}, []string{"TIMESTAMPTZ", "TIMETZ", "TIMESTAMPTZ", "TIMETZ"}, [][]any{{nil, nil, nil, nil}}))
		timezoneBoth(t, p, fixture.Exec("UPDATE "+table+" SET a='2004-01-01 00:00:00.1236+00',b='10:00:00.1236-05',c='2004-01-01 00:00:00.1236+00',d='10:00:00.1236-05'", 1))
		timezoneBoth(t, p, fixture.Select("SELECT a::TEXT AS a,b::TEXT AS b,c::TEXT AS c,d::TEXT AS d FROM "+table, []string{"a", "b", "c", "d"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"2004-01-01 00:00:00.124+00", "10:00:00.124-05", "2004-01-01 00:00:00.1236+00", "10:00:00.1236-05"}}))
	}
}

func TestReferenceTimezoneRelativeInputClocks(t *testing.T) {
	p := fixture.TimezoneReferencePair(t)
	for _, backend := range p.Backends() {
		for _, zone := range []struct {
			name    string
			seconds int
		}{{"UTC", 0}, {"UTC-2", 7200}} {
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Exec("SET TIME ZONE '"+zone.name+"'", 0))
			tx, err := backend.DB.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			clock := timezoneTransactionClock(t, p, tx, backend.Recorder)
			local := clock.UTC().Add(time.Duration(zone.seconds) * time.Second)
			for _, tc := range []struct {
				keyword string
				days    int
			}{{"yesterday", -1}, {"today", 0}, {"tomorrow", 1}} {
				want := local.AddDate(0, 0, tc.days).Format("2006-01-02") + " 00:00:00"
				for _, prepared := range []bool{false, true} {
					s := timezoneTextStep("TIMESTAMPTZ '"+tc.keyword+"' AT LOCAL", want)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, tx, backend.Recorder, s)
				}
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
