package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceRangeTypeConstructorFloat4(t *testing.T) {
	rangeTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, conn *sql.Conn, run func(fixture.Step)) {
		// Use the existing public raw32 binary-compatible CAST seam. Text NaN
		// cannot independently prove preservation of these distinct payloads.
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := conn.ExecContext(ctx, `DROP CAST IF EXISTS (real AS xid); DROP CAST IF EXISTS (xid AS real)`); err != nil {
				t.Error("raw32 cast cleanup", err)
			}
		}()
		for _, q := range []string{
			`CREATE CAST (xid AS real) WITHOUT FUNCTION`,
			`CREATE CAST (real AS xid) WITHOUT FUNCTION`,
			`CREATE TYPE constructor_float AS RANGE(SUBTYPE=real,MULTIRANGE_TYPE_NAME=constructor_floats)`,
			`CREATE TYPE constructor_float_box AS (r constructor_float,m constructor_floats)`,
			`CREATE DOMAIN constructor_float_domain AS constructor_float`,
			`CREATE TABLE constructor_float_data(k integer,r constructor_float,m constructor_floats,a constructor_float[],c constructor_float_box,d constructor_float_domain)`,
			`CREATE TABLE constructor_float_defaults(r constructor_float DEFAULT constructor_float(('2139095041'::xid)::real,('2139095041'::xid)::real,'[]'))`,
			`CREATE VIEW constructor_float_literal AS SELECT constructor_float(('2143289345'::xid)::real,('2143289345'::xid)::real,'[]') AS r`,
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec(`INSERT INTO constructor_float_defaults DEFAULT VALUES`, 1))
		words := []string{"2143289345", "2139095041", "2147483648", "1065353216"}
		insert := `INSERT INTO constructor_float_data VALUES($1,constructor_float(($2::xid)::real,($2::xid)::real,'[]'),constructor_floats(constructor_float(($2::xid)::real,($2::xid)::real,'[]')),ARRAY[constructor_float(($2::xid)::real,($2::xid)::real,'[]')],ROW(constructor_float(($2::xid)::real,($2::xid)::real,'[]'),constructor_floats(constructor_float(($2::xid)::real,($2::xid)::real,'[]')))::constructor_float_box,constructor_float(($2::xid)::real,($2::xid)::real,'[]')::constructor_float_domain)`
		var want [][]any
		for i, word := range words {
			run(fixture.Exec(insert, 1, int64(i), word))
			want = append(want, []any{int64(i), word, word, word, word, word, word})
		}
		stmt, err := conn.PrepareContext(p.Ctx, insert)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		for i, word := range words {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: insert}, b.Recorder, fixture.Exec(insert, 1, int64(i+len(words)), word))
			want = append(want, []any{int64(i + len(words)), word, word, word, word, word, word})
		}
		q := `SELECT k,lower(r)::xid::text AS lo,upper(r)::xid::text AS hi,lower(range_merge(m))::xid::text AS multi_lo,lower(a[1])::xid::text AS array_lo,upper((c).r)::xid::text AS composite_hi,lower(d)::xid::text AS domain_lo FROM constructor_float_data ORDER BY k`
		check := func(backend fixture.Backend, exec fixture.Executor) {
			t.Helper()
			for _, prepared := range []bool{false, true} {
				s := fixture.Select(q, []string{"k", "lo", "hi", "multi_lo", "array_lo", "composite_hi", "domain_lo"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, want)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, exec, backend.Recorder, s)
			}
			for _, tc := range []struct{ table, word string }{{"constructor_float_defaults", "2139095041"}, {"constructor_float_literal", "2143289345"}} {
				fixture.RunStep(t, p.Ctx, exec, backend.Recorder, fixture.Select(`SELECT lower(r)::xid::text AS lo,upper(r)::xid::text AS hi FROM `+tc.table, []string{"lo", "hi"}, []string{"TEXT", "TEXT"}, [][]any{{tc.word, tc.word}}))
			}
		}
		check(b, conn)
		saved := fixture.Select(q, []string{"k", "lo", "hi", "multi_lo", "array_lo", "composite_hi", "domain_lo"}, []string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, want)
		after := saved
		after.Rows = append(append([][]any(nil), want...), []any{int64(8), words[0], words[0], words[0], words[0], words[0], words[0]})
		rangeTypeConstructorReplay(t, p, b, conn, true, check, saved, fixture.Prepared(fixture.Exec(insert, 1, int64(8), words[0])), after)
	})
}
