package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const pwSignature = "pw_part.pg_prewarm(regclass,text,text,bigint,bigint)"
const pwCall = "SELECT pw_part.pg_prewarm($1::regclass,$2::text,$3::text,$4::bigint,$5::bigint) AS warmed"

func pwScenario(t *testing.T, install bool, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	for _, name := range []string{"pw_part", "pw_target", "pw_moved", "bc_part"} {
		fixture.SchemaReferenceCleanup(t, name)
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			if b.PG {
				clean := func() {
					ctx, cancel := context.WithTimeout(context.Background(), fixture.Liveness)
					defer cancel()
					for _, q := range []string{"ROLLBACK", "RESET ROLE", "RESET SESSION AUTHORIZATION", "RESET search_path", "DROP EXTENSION IF EXISTS pg_prewarm CASCADE", "DROP EXTENSION IF EXISTS pg_buffercache CASCADE", "DROP SCHEMA IF EXISTS pw_part CASCADE", "DROP SCHEMA IF EXISTS pw_target CASCADE", "DROP SCHEMA IF EXISTS pw_moved CASCADE", "DROP SCHEMA IF EXISTS bc_part CASCADE"} {
						if _, err := c.ExecContext(ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			bcDDL(t, p, b, c, "CREATE SCHEMA pw_part")
			if install {
				bcDDL(t, p, b, c, "CREATE EXTENSION pg_prewarm SCHEMA pw_part", "CREATE TABLE pw_part.heap(k integer)", "ALTER TABLE pw_part.heap SET(autovacuum_enabled=false)")
			}
			body(t, p, b, c)
		})
	}
}

func pwCount(query string, n int64, args ...any) fixture.Step {
	return fsMetadata(fixture.Select(query, []string{"warmed"}, []string{"INT8"}, [][]any{{n}}, args...), false)
}

// Each invocation has independent expected rows and complete metadata. A held
// route is additional to a fresh preparation, never a substitute for it.
func pwRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	bcBoth(t, p, b, c, s)
	st, err := c.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal(s.Query, err)
	}
	defer st.Close()
	fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: s.Query}, b.Recorder, s)
}

func pwError(query, code, message string, args ...any) fixture.Step {
	s := fixture.ErrorDiagnostic(query, fixture.ControlsReferenceDiagnostic(code, message), args...)
	s.SelectRows = true
	return s
}

func pwBodyError(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	s.ErrorStage = "execute"
	pwRoutes(t, p, b, c, s)
}

func pwInsert(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string, n int) {
	t.Helper()
	values := make([]string, n)
	for i := range values {
		values[i] = fmt.Sprintf("(%d)", i+1)
	}
	bcRun(t, p, b, c, fixture.Exec("INSERT INTO "+table+" VALUES"+strings.Join(values, ","), int64(n)))
}

func pwExtent(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table, fork string, native int64) int64 {
	t.Helper()
	if !b.PG {
		return native
	}
	var bytes, blockSize int64
	if err := c.QueryRowContext(p.Ctx, "SELECT pg_relation_size($1::regclass,$2::text),current_setting('block_size')::bigint", table, fork).Scan(&bytes, &blockSize); err != nil {
		t.Fatal(err)
	}
	if blockSize != 8192 || bytes < 0 || bytes%blockSize != 0 {
		t.Fatal("independent physical extent", bytes, blockSize)
	}
	return bytes / blockSize
}

func pwCacheInstall(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	bcDDL(t, p, b, c, "CREATE SCHEMA bc_part", "CREATE EXTENSION pg_buffercache SCHEMA bc_part")
}

func pwCoordinates(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string) map[[2]int64][]any {
	t.Helper()
	result := make(map[[2]int64][]any)
	for _, row := range bcTarget(t, p, b, c, table) {
		key := [2]int64{row[4].(int64), row[5].(int64)}
		if result[key] != nil {
			t.Fatal("duplicate resident coordinate", key)
		}
		if row[7].(int64) < 0 || row[7].(int64) > 5 || row[8] != int64(0) {
			t.Fatal("unreleased pin or invalid usage", row)
		}
		result[key] = row
	}
	return result
}

func pwResident(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, table string, fork, first, last int64) {
	t.Helper()
	rows := pwCoordinates(t, p, b, c, table)
	for block := first; block <= last; block++ {
		if rows[[2]int64{fork, block}] == nil {
			t.Fatal("requested block not resident", table, fork, block, rows)
		}
	}
}

func pwSameCoordinates(t *testing.T, before, after map[[2]int64][]any) {
	t.Helper()
	if len(before) != len(after) {
		t.Fatal("coordinate count changed", before, after)
	}
	for key, row := range before {
		if !reflect.DeepEqual(row, after[key]) {
			t.Fatal("resident coordinate changed without a read", key, row, after[key])
		}
	}
}

func pwViewCount(query string, n int64, pg bool) fixture.Step {
	s := fixture.Select(query, []string{"warmed"}, []string{"INT8"}, [][]any{{n}})
	s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
		bcMetadata(t, rows, pg, true, s.Columns, s.Types)
	}
	return s
}
