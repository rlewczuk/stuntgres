package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func externalSQLProviderConnection(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Errorf("external SQL consumer close: %v", err)
		}
	})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		for _, q := range []string{
			"ROLLBACK",
			"DROP SERVER IF EXISTS external_sql_live CASCADE",
			"DROP FOREIGN DATA WRAPPER IF EXISTS external_sql_native CASCADE",
			"DROP FOREIGN DATA WRAPPER IF EXISTS external_sql_metadata CASCADE",
		} {
			if _, err := c.ExecContext(ctx, q); err != nil {
				t.Errorf("external SQL cleanup %s: %v", q, err)
			}
		}
	})
	return c
}

// These are consumer values, not rows supplied by a native callback. The physical
// scenario stores them in the genuine PostgreSQL source; the Docker-free contract
// scenario stores them only in its explicitly local sentinel table.
func externalSQLProviderExpectedRows() [][]any {
	return [][]any{
		{int64(1), "x", true, "\x00\xff"},
		{int64(2), nil, false, nil},
		{int64(2), nil, false, nil},
		{int64(3), "", nil, ""},
	}
}

func externalSQLProviderRows(postgres bool, query string, values [][]any, args ...any) fixture.Step {
	names := []string{"id", "label", "flag", "bytes"}
	types := []string{"INT4", "TEXT", "BOOL", "BYTEA"}
	s := fixture.Select(query, names, types, values, args...)
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		fixture.TableDDLColumnTypes(t, postgres, columns, names, types, []bool{false, true, true, true})
	}
	return s
}

// The same public SQL and independent expectations apply to the local contract
// table and the genuinely external foreign table. Descriptors are checked before
// iteration, including empty and NULL-parameter results, on every execution route.
func externalSQLProviderValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	base := "SELECT id,label,flag,bytes FROM " + table
	for _, prepared := range []bool{false, true} {
		bag := externalSQLProviderRows(b.PG, base, externalSQLProviderExpectedRows())
		bag.Bag, bag.Prepared = true, prepared
		run(bag)
		ordered := externalSQLProviderRows(b.PG, base+" ORDER BY id", externalSQLProviderExpectedRows())
		ordered.Prepared = prepared
		run(ordered)
		for _, tc := range []struct {
			arg  any
			rows [][]any
		}{
			{int64(2), [][]any{{int64(2), nil, false, nil}, {int64(2), nil, false, nil}}},
			{int64(99), nil},
			{nil, nil},
		} {
			s := externalSQLProviderRows(b.PG, base+" WHERE id=$1 ORDER BY id", tc.rows, tc.arg)
			s.Prepared = prepared
			run(s)
		}
	}

	q := base + " WHERE id=$1 ORDER BY id"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			t.Errorf("external SQL held statement close: %v", err)
		}
	}()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for _, tc := range []struct {
		arg  any
		rows [][]any
	}{
		{int64(1), [][]any{{int64(1), "x", true, "\x00\xff"}}},
		{int64(2), [][]any{{int64(2), nil, false, nil}, {int64(2), nil, false, nil}}},
		{int64(3), [][]any{{int64(3), "", nil, ""}}},
		{int64(99), nil},
		{nil, nil},
		{int64(1), [][]any{{int64(1), "x", true, "\x00\xff"}}},
	} {
		fixture.RunStep(t, p.Ctx, held, b.Recorder, externalSQLProviderRows(b.PG, q, tc.rows, tc.arg))
	}
}
