//go:build reference

package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestReferenceTimezoneRangeAndUpdateAtomicity(t *testing.T) {
	p := timezoneReferencePair(t)
	for _, input := range []string{"4714-11-24 00:00+01 BC", "294276-12-31 23:59:59.999999-01"} {
		q := "SELECT TIMESTAMPTZ '" + input + "' FROM tz_witness"
		s := referenceImplicitError(q, referenceDiagnostic{Code: "22008", Message: `timestamp out of range: "` + input + `"`, Position: strconv.Itoa(strings.Index(q, "'") + 1)})
		s.selectRows = true
		s.errorStage = "execute"
		p.step(t, s)
		s.errorStage = "prepare"
		p.step(t, referencePrepared(s))
	}
	// Root's pinned18.0 oracle observed this typmod carry is accepted, even
	// though fresh input of the resulting year lies beyond the input boundary.
	timezoneBoth(t, p, timezoneTextStep("(TIMESTAMPTZ '294276-12-31 23:59:59.999999+00')::TIMESTAMPTZ(0)", "294277-01-01 00:00:00+00"))
	p.step(t, referenceExec("UPDATE tz_witness SET z='epoch' WHERE id=1", 1))
	p.step(t, referenceExec("INSERT INTO tz_witness(id,z) VALUES(2,'epoch')", 1))
	sentinel := referenceSelect("SELECT id,z::TEXT AS z FROM tz_witness ORDER BY id", []string{"id", "z"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "1970-01-01 00:00:00+00"}, {int64(2), "1970-01-01 00:00:00+00"}})
	s := referenceImplicitError("UPDATE tz_witness SET z=(CASE WHEN id=1 THEN '2004-01-01+00' ELSE $1::TEXT END)::TIMESTAMPTZ", referenceDiagnostic{Code: "22007", Message: `invalid input syntax for type timestamp with time zone: "nonsense"`}, "nonsense")
	s.errorStage = "execute"
	timezoneBoth(t, p, s)
	timezoneBoth(t, p, sentinel)
	q := "UPDATE tz_witness SET z='infinity'; nonsense"
	s = referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: `syntax error at or near "nonsense"`, Position: strconv.Itoa(strings.Index(q, "nonsense") + 1)})
	s.errorStage = "execute"
	p.step(t, s)
	s.errorStage = "prepare"
	p.step(t, referencePrepared(s))
	timezoneBoth(t, p, sentinel)
	for _, backend := range []struct {
		db     *sql.DB
		record *referenceRecorder
	}{{p.memory, p.memoryRecord}, {p.postgres, p.postgresRecord}} {
		// Context canceled after query creation must not invent an SQL diagnostic.
		// pq may already have buffered a complete wire result; only the native
		// lazy cursor promises cancellation before its next exported row.
		if backend.db != p.memory {
			continue
		}
		ctx, cancel := context.WithCancel(p.ctx)
		rows, err := backend.db.QueryContext(ctx, "SELECT z FROM tz_witness ORDER BY id")
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
		referenceRunStep(t, p.ctx, backend.db, backend.record, sentinel)
	}
}

func TestReferenceTimezoneRelativeInputAndDeclarations(t *testing.T) {
	p := timezoneReferencePair(t)
	p.step(t, referenceExec("CREATE TABLE tz_spelling(a TIMESTAMP(3) WITH TIME ZONE,b TIME(3) WITH TIME ZONE,c pg_catalog.timestamptz,d pg_catalog.timetz)", 0))
	p.step(t, referencePrepared(referenceExec("CREATE TABLE tz_spelling_prepared(a TIMESTAMP(3) WITH TIME ZONE,b TIME(3) WITH TIME ZONE,c pg_catalog.timestamptz,d pg_catalog.timetz)", 0)))
	timezoneBoth(t, p, referenceSelect("SELECT a,b,c,d FROM tz_spelling", []string{"a", "b", "c", "d"}, []string{"TIMESTAMPTZ", "TIMETZ", "TIMESTAMPTZ", "TIMETZ"}, nil))
	// Check NULL column typmods against PostgreSQL's actual catalog, not the
	// driver's intentionally nonnumeric DecimalSize adapter metadata.
	timezoneBoth(t, p, referenceSelect("SELECT attname,atttypmod FROM pg_attribute WHERE attrelid='tz_spelling'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypmod"}, []string{"NAME", "INT4"}, [][]any{{"a", int64(3)}, {"b", int64(3)}, {"c", int64(-1)}, {"d", int64(-1)}}))
	for _, zone := range []struct {
		name    string
		seconds int
	}{{"UTC", 0}, {"UTC-2", 7200}} {
		p.step(t, referenceExec("SET TIME ZONE '"+zone.name+"'", 0))
		pg, err := p.postgres.BeginTx(p.ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		var clock time.Time
		if err := pg.QueryRowContext(p.ctx, "SELECT transaction_timestamp()").Scan(&clock); err != nil {
			pg.Rollback()
			t.Fatal(err)
		}
		native, err := p.memory.BeginTx(p.ctx, nil)
		if err != nil {
			pg.Rollback()
			t.Fatal(err)
		}
		local := clock.UTC().Add(time.Duration(zone.seconds) * time.Second)
		now := time.Now().UTC().Add(time.Duration(zone.seconds) * time.Second)
		if local.Format("2006-01-02") != now.Format("2006-01-02") {
			native.Rollback()
			pg.Rollback()
			t.Fatal("transaction starts straddle local midnight")
		}
		for _, tc := range []struct {
			keyword string
			days    int
		}{{"yesterday", -1}, {"today", 0}, {"tomorrow", 1}} {
			want := local.AddDate(0, 0, tc.days).Format("2006-01-02") + " 00:00:00"
			for _, prepared := range []bool{false, true} {
				s := timezoneTextStep("TIMESTAMPTZ '"+tc.keyword+"' AT LOCAL", want)
				s.prepared = prepared
				referenceRunStep(t, p.ctx, native, p.memoryRecord, s)
				referenceRunStep(t, p.ctx, pg, p.postgresRecord, s)
			}
		}
		if err := native.Rollback(); err != nil {
			t.Fatal(err)
		}
		if err := pg.Rollback(); err != nil {
			t.Fatal(err)
		}
	}
}
