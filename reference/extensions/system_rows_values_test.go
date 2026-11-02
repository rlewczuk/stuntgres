package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemRowsCounts(t *testing.T) {
	srCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, k := range []int64{0, 1, 3, 10, 11, 9223372036854775807} {
			t.Run(fmt.Sprintf("k%d", k), func(t *testing.T) {
				n := min(k, int64(10))
				srBoth(t, p, b, c, srSubset("sr", "system_rows", fmt.Sprint(k), n))
				srBoth(t, p, b, c, srSubset("sr", "system_rows", "$1", n, k))
				if k >= 10 {
					srBoth(t, p, b, c, srMetadata(fixture.Select("SELECT id,payload FROM sr TABLESAMPLE system_rows($1) ORDER BY id", srNames, srTypes, srRows(), k), b.PG))
				}
			})
		}
		for _, table := range []string{"sr", "sr_empty"} {
			for _, k := range []int64{0, 3} {
				if table == "sr" && k != 0 {
					continue
				}
				srBoth(t, p, b, c, srMetadata(fixture.Select("SELECT id,payload FROM "+table+" TABLESAMPLE system_rows($1)", srNames, srTypes, nil, k), b.PG))
			}
		}
	})
}

func TestReferenceSystemRowsArguments(t *testing.T) {
	srCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, argument := range []string{"'2'", "2::bigint", "1.6", "1.6::numeric"} {
			t.Run(argument, func(t *testing.T) {
				srBoth(t, p, b, c, srSubset("sr", "system_rows", argument, 2))
			})
		}
		q := "SELECT count(*) AS n FROM sr TABLESAMPLE system_rows($1)"
		h := srHeld(t, p, c, q)
		for _, k := range []int64{0, 3, 11} {
			srRun(t, p, b, h, srCount(q, min(k, int64(10)), k))
		}
		srRun(t, p, b, h, srCount(q, 2, "2"))
		srRun(t, p, b, h, srCount(q, 10, "9223372036854775807"))
		for _, item := range []struct {
			value   any
			message string
		}{{nil, "TABLESAMPLE parameter cannot be null"}, {int64(-1), "sample size must not be negative"}} {
			srRun(t, p, b, h, srError(q, "2202H", item.message, 0, item.value))
		}
		srRun(t, p, b, h, srCount(q, 3, int64(3)))
		// The count is correlated to the preceding source, not prohibited as a
		// relation reference or treated as a query-global LIMIT.
		// Use existing scalar correlation syntax: explicit derived-table LATERAL
		// support is an adjacent query feature, not a sampling prerequisite.
		correlated := "SELECT limits.k,(SELECT count(*) FROM sr TABLESAMPLE system_rows(limits.k)) AS n FROM (VALUES(0),(3),(11)) AS limits(k) ORDER BY limits.k"
		srBoth(t, p, b, c, fixture.Select(correlated, []string{"k", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(0)}, {int64(3), int64(3)}, {int64(11), int64(10)}}))
		t.Run("visible_versions", func(t *testing.T) {
			tx, err := c.BeginTx(p.Ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			srRun(t, p, b, tx, fixture.Exec("DELETE FROM sr WHERE id IN(2,4)", 2))
			srRun(t, p, b, tx, fixture.Exec("UPDATE sr SET payload='changed' WHERE id=3", 1))
			srRun(t, p, b, tx, fixture.Exec("INSERT INTO sr VALUES(11,'private')", 1))
			want := [][]any{{int64(1), "v1"}, {int64(3), "changed"}, {int64(5), "v5"}, {int64(6), "v6"}, {int64(7), "v7"}, {int64(8), "v8"}, {int64(9), "v9"}, {int64(10), "v10"}, {int64(11), "private"}}
			srBoth(t, p, b, tx, srMetadata(fixture.Select("SELECT id,payload FROM sr TABLESAMPLE system_rows(9223372036854775807) ORDER BY id", srNames, srTypes, want), b.PG))
			srBoth(t, p, b, tx, srCount("SELECT count(*) AS n FROM sr TABLESAMPLE system_rows(3)", 3))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			srBoth(t, p, b, c, fixture.Select("SELECT id,payload FROM sr TABLESAMPLE system_rows(11) ORDER BY id", srNames, srTypes, srRows()))
		})
	})
}
