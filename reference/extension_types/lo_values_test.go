package reference_test

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLoValues(t *testing.T) {
	loScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		loInstall(t, p, b, c)
		loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_values(id integer NOT NULL,v lo)", 0))
		loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_values VALUES (1,'0'),(2,'1'),(3,'2147483647'),(4,'2147483648'),(5,'4294967295'),(6,NULL),(7,'4294967295'),(8,'26')", 8))
		want := [][]any{{int64(1), int64(0)}, {int64(2), int64(1)}, {int64(3), int64(2147483647)}, {int64(4), int64(2147483648)}, {int64(5), int64(4294967295)}, {int64(6), nil}, {int64(7), int64(4294967295)}, {int64(8), int64(26)}}
		for _, prepared := range []bool{false, true} {
			s := fixture.Select("SELECT id,v FROM lo_values ORDER BY id", []string{"id", "v"}, []string{"INT4", "OID"}, want)
			s.Prepared = prepared
			loRows(t, p, b, c, s)
			s = fixture.Select("SELECT v::text AS canonical,v::oid::bigint AS positive FROM lo_values ORDER BY id", []string{"canonical", "positive"}, []string{"TEXT", "INT8"}, [][]any{{"0", int64(0)}, {"1", int64(1)}, {"2147483647", int64(2147483647)}, {"2147483648", int64(2147483648)}, {"4294967295", int64(4294967295)}, {nil, nil}, {"4294967295", int64(4294967295)}, {"26", int64(26)}})
			s.Prepared = prepared
			loRun(t, p, b, c, s)
		}
		loRun(t, p, b, c, fixture.Exec("CREATE TABLE lo_one(id integer)", 0))
		loRun(t, p, b, c, fixture.Exec("INSERT INTO lo_one VALUES (1)", 1))
		for _, tc := range []struct {
			input string
			want  int64
		}{
			{"0377", 255}, {"0xffffffff", 4294967295}, {"\t\n\r\v\f +0007 \t", 7}, {"-1", 4294967295}, {"-2147483648", 2147483648}, {"18446744073709551615", 4294967295}, {"18446744071562067968", 2147483648},
		} {
			for _, prepared := range []bool{false, true} {
				s := fixture.Select("SELECT '"+tc.input+"'::lo AS v FROM lo_one", []string{"v"}, []string{"OID"}, [][]any{{tc.want}})
				s.Prepared = prepared
				loRows(t, p, b, c, s)
			}
		}
		// PG's large-object catalog independently establishes a dangling value.
		// Shared storage/update/delete witnesses do not require native LO APIs.
		if b.PG {
			loRun(t, p, b, c, fixture.Select("SELECT count(*) FROM pg_largeobject_metadata WHERE oid='4294967295'::oid", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		for _, prepared := range []bool{false, true} {
			q := "SELECT v FROM lo_values WHERE id=5"
			executor := fixture.Executor(c)
			if prepared {
				executor = loHeld(t, p, c, q)
			}
			for _, tc := range []struct {
				dest any
				want any
			}{
				{new(int64), int64(4294967295)}, {new(string), "4294967295"}, {new([]byte), []byte("4294967295")}, {new(sql.NullInt64), sql.NullInt64{Int64: 4294967295, Valid: true}},
			} {
				rows, err := executor.QueryContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				if !rows.Next() {
					rows.Close()
					t.Fatal("missing scan row")
				}
				if err := rows.Scan(tc.dest); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if !reflect.DeepEqual(reflect.ValueOf(tc.dest).Elem().Interface(), tc.want) {
					t.Fatal("scan", tc.dest, tc.want)
				}
				if rows.Next() || rows.Err() != nil {
					t.Fatal("scan cardinality", rows.Err())
				}
				rows.Close()
			}
		}
		var null sql.NullInt64
		if err := c.QueryRowContext(p.Ctx, "SELECT v FROM lo_values WHERE id=6").Scan(&null); err != nil || null.Valid {
			t.Fatal("NULL scan", null, err)
		}
		var nonnull int64
		err := c.QueryRowContext(p.Ctx, "SELECT v FROM lo_values WHERE id=6").Scan(&nonnull)
		var state interface{ SQLState() string }
		if err == nil || errors.As(err, &state) {
			t.Fatal("ordinary NULL scan error", err)
		}
		loRun(t, p, b, c, fixture.Exec("DELETE FROM lo_values WHERE id=5", 1))
		loRows(t, p, b, c, fixture.Select("SELECT v FROM lo_values WHERE id=7", []string{"v"}, []string{"OID"}, [][]any{{int64(4294967295)}}))
		loRun(t, p, b, c, fixture.Exec("UPDATE lo_values SET v='0' WHERE id=7", 1))
		loRows(t, p, b, c, fixture.Prepared(fixture.Select("SELECT v FROM lo_values WHERE id=7", []string{"v"}, []string{"OID"}, [][]any{{int64(0)}})))
	})
}
