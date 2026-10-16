package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGCursorsMetadata(t *testing.T) {
	pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pgcEmpty(t, p, b, c, "pgc_plain")
		q := pgcQuery("pgc_plain")
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		pgcEmpty(t, p, b, held, "pgc_plain")
		sqlCursorSetup(t, p, b, c)
		sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
		plain := "DECLARE pgc_plain NO SCROLL CURSOR WITHOUT HOLD FOR SELECT 1 AS i"
		hold := "DECLARE pgc_hold SCROLL CURSOR WITH HOLD FOR SELECT 2 AS i"
		sqlCursorStep(t, p, b, c, fixture.Exec(plain, 0))
		sqlCursorStep(t, p, b, c, fixture.Exec(hold, 0))
		pgcOne(t, p, b, c, "pgc_plain", plain, false, false)
		pgcOne(t, p, b, held, "pgc_plain", plain, false, false)
		pgcOne(t, p, b, c, "pgc_hold", hold, true, true)
		omitted := "DECLARE pgc_omitted CURSOR FOR SELECT i FROM cursor_rows ORDER BY i"
		// The sort plan supports backward scan on PostgreSQL; native omitted
		// SCROLL intentionally remains forward-only, unlike explicit SCROLL.
		sqlCursorStep(t, p, b, c, fixture.Exec(omitted, 0))
		pgcOne(t, p, b, c, "pgc_omitted", omitted, false, b.PG)
		sqlCursorStep(t, p, b, c, fixture.Select("SELECT relkind,relnatts FROM pg_catalog.pg_class WHERE relnamespace=11 AND relname='pg_cursors'", []string{"relkind", "relnatts"}, []string{"CHAR", "INT2"}, [][]any{{"v", int64(6)}}))
		if b.PG {
			// Supplementary oracle catalog relationships; these adjacent catalog
			// surfaces are not native features. Native ownership is checked by
			// TestPGCursorsCatalog through the actual catalog descriptor APIs.
			sqlCursorStep(t, p, b, c, fixture.Select("SELECT t.typrelid=c.oid AS row_link,a.typelem=t.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.relnamespace=11 AND c.relname='pg_cursors'", []string{"row_link", "array_link"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		}
		sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE pgc_plain", 0))
		pgcEmpty(t, p, b, held, "pgc_plain")
	})
}

func TestReferencePGCursorsSourcesAndTime(t *testing.T) {
	t.Run("direct_prepared_zone", func(t *testing.T) {
		pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			sqlCursorStep(t, p, b, c, fixture.Exec("SET TimeZone='UTC'", 0))
			sqlCursorStep(t, p, b, c, fixture.Exec("BEGIN", 0))
			source := " \n/* original declaration */ DECLARE \"pgc_text\" NO SCROLL CURSOR FOR SELECT 7 AS i;  \n"
			before := pgcClock(t, p, c)
			sqlCursorStep(t, p, b, c, fixture.Exec(source, 0))
			after := pgcClock(t, p, c)
			original := pgcOne(t, p, b, c, "pgc_text", source, false, false)
			pgcBracket(t, original.Created, before, after)
			prepared := "/* parameter retained */ DECLARE pgc_param SCROLL CURSOR FOR SELECT $1::integer AS i"
			stmt, err := c.PrepareContext(p.Ctx, prepared)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			pgcEmpty(t, p, b, c, "pgc_param")
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: prepared}
			for _, argument := range []int64{11, 22} {
				before = pgcClock(t, p, c)
				sqlCursorStep(t, p, b, held, fixture.Exec(prepared, 0, argument))
				after = pgcClock(t, p, c)
				row := pgcOne(t, p, b, c, "pgc_param", prepared, false, true)
				pgcBracket(t, row.Created, before, after)
				sqlCursorStep(t, p, b, c, fixture.Select("FETCH 1 FROM pgc_param", []string{"i"}, []string{"INT4"}, [][]any{{argument}}))
				pgcSame(t, p, b, c, row)
				sqlCursorStep(t, p, b, c, fixture.Exec("CLOSE pgc_param", 0))
			}
			sqlCursorStep(t, p, b, c, fixture.Exec("SET TimeZone='Asia/Kathmandu'", 0))
			changed := pgcOne(t, p, b, c, "pgc_text", source, false, false)
			_, offset := changed.Created.Zone()
			_, originalOffset := original.Created.Zone()
			if !changed.Created.Equal(original.Created) || offset != 20700 || originalOffset != 0 {
				t.Fatalf("timezone original=%v changed=%v", original.Created, changed.Created)
			}
		})
	})
	t.Run("message_source_and_time", func(t *testing.T) {
		pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			q := "BEGIN; /* whole message */ DECLARE pgc_batch_a NO SCROLL CURSOR FOR SELECT 1; DECLARE pgc_batch_b SCROLL CURSOR FOR SELECT 2; SELECT name,statement,creation_time=statement_timestamp() AS same_start FROM pg_cursors WHERE name IN ('pgc_batch_a','pgc_batch_b') ORDER BY name;"
			r, err := c.QueryContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			fixture.BatchSets(t, r, fixture.BatchSet{Columns: []string{"name", "statement", "same_start"}, Types: []string{"TEXT", "TEXT", "BOOL"}, Rows: [][]any{{"pgc_batch_a", q, true}, {"pgc_batch_b", q, true}}})
			a := pgcOne(t, p, b, c, "pgc_batch_a", q, false, false)
			bb := pgcOne(t, p, b, c, "pgc_batch_b", q, false, true)
			if !a.Created.Equal(bb.Created) {
				t.Fatal("one message has different creation timestamps")
			}
		})
	})
	t.Run("failed_batches", func(t *testing.T) {
		pgcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct{ query, state string }{
				{"BEGIN; DECLARE pgc_bad NO SCROLL CURSOR FOR SELECT 1; SELECT 1/0;", "22012"},
				{"BEGIN; DECLARE pgc_bad NO SCROLL CURSOR FOR SELECT 1; SELECT FROM;", "42601"},
			} {
				result, err := c.ExecContext(p.Ctx, tc.query)
				if result != nil {
					t.Fatal("failed batch published Result")
				}
				fixture.State(t, err, tc.state)
				if tc.state == "42601" {
					// Parsing the whole input fails before BEGIN or DECLARE runs.
					pgcEmpty(t, p, b, c, "pgc_bad")
				} else {
					sqlCursorStep(t, p, b, c, fixture.ErrorDiagnostic(pgcQuery("pgc_bad"), sqlCursorDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
				}
				sqlCursorStep(t, p, b, c, fixture.Exec("ROLLBACK", 0))
				pgcEmpty(t, p, b, c, "pgc_bad")
			}
		})
	})
}
