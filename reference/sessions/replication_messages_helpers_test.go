package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

const replicationEmitUnsupported = "pg_logical_emit_message logical WAL publication is unsupported in embedded mode"
const replicationSyncUnsupported = "pg_sync_replication_slots physical replication slot synchronization is unsupported in embedded mode"

var replicationMessageIdentities = []string{"pg_logical_emit_message(boolean,text,text,boolean)", "pg_logical_emit_message(boolean,text,bytea,boolean)", "pg_sync_replication_slots()"}

func replicationMessageError(query, code, message, detail string) fixture.Step {
	s := fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message, Detail: detail})
	s.SelectRows, s.ErrorStage = true, "execute"
	return s
}
func replicationMessageSync(query string, pg, authorized bool) fixture.Step {
	if !authorized {
		return replicationMessageError(query, "42501", "permission denied to use replication slots", "Only roles with the REPLICATION attribute may use replication slots.")
	}
	if pg {
		return replicationMessageError(query, "55000", "replication slots can only be synchronized to a standby server", "")
	}
	return replicationMessageError(query, "0A000", replicationSyncUnsupported, "")
}
func replicationMessageNull(query string, args ...any) fixture.Step {
	return fixture.Select(query, []string{"emitted"}, []string{"PG_LSN"}, [][]any{{nil}}, args...)
}
func replicationMessageRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(s))
	recoveryHeld(t, p, b, c, s)
}

// A physical LSN is deliberately a property, never a golden allocator address.
func replicationMessageEmit(t *testing.T, p *fixture.Pair, b fixture.Backend, executor fixture.Executor, query string, args ...any) {
	t.Helper()
	if !b.PG {
		s := replicationMessageError(query, "0A000", replicationEmitUnsupported, "")
		s.Args = args
		fixture.RunStep(t, p.Ctx, executor, b.Recorder, s)
		return
	}
	rows, err := executor.QueryContext(p.Ctx, query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 1 || columns[0].DatabaseTypeName() != "PG_LSN" {
		t.Fatal("real LSN descriptor", columns, err)
	}
	if !rows.Next() {
		t.Fatal("missing real LSN", rows.Err())
	}
	var value string
	if err := rows.Scan(&value); err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(value, "/")
	if len(parts) != 2 {
		t.Fatal("noncanonical LSN", value)
	}
	var words [2]uint64
	for i, part := range parts {
		word, err := strconv.ParseUint(part, 16, 32)
		if err != nil || strings.ToUpper(strconv.FormatUint(word, 16)) != part {
			t.Fatal("noncanonical LSN", value, err)
		}
		words[i] = word
	}
	if words == [2]uint64{} {
		t.Fatal("invented zero LSN")
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("scalar LSN cardinality", rows.Err())
	}
}
func replicationMessageEmitRoutes(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, query string, args ...any) {
	t.Helper()
	replicationMessageEmit(t, p, b, c, query, args...)
	// A fresh Prepare and a second execution on the same handle distinguish
	// metadata-only preparation from volatile execution and held reuse.
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal("valid call Prepare", err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
	replicationMessageEmit(t, p, b, held, query, args...)
	replicationMessageEmit(t, p, b, held, query, args...)
}
func replicationMessagesMetadata(pg bool, names []string) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		columns, err := rows.ColumnTypes()
		if err != nil || len(columns) != 3 {
			t.Fatal(columns, err)
		}
		for i, c := range columns {
			typ := "PG_LSN"
			if i == 2 {
				typ = "VOID"
			}
			scan := reflect.TypeFor[string]()
			if pg {
				scan = reflect.TypeFor[any]()
			}
			if c.Name() != names[i] || c.DatabaseTypeName() != typ || c.ScanType() != scan {
				t.Fatal("released result metadata", i, c.Name(), c.DatabaseTypeName(), c.ScanType())
			}
			if n, ok := c.Nullable(); n || ok {
				t.Fatal("computed nullability", n, ok)
			}
			if _, ok := c.Length(); ok {
				t.Fatal("unexpected length")
			}
			if _, _, ok := c.DecimalSize(); ok {
				t.Fatal("unexpected precision")
			}
		}
	}
}
func replicationMessagesRestore(t *testing.T, c *sql.Conn, pg bool, roles ...string) func() {
	t.Helper()
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := c.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Error(err)
			return
		}
		for _, identity := range replicationMessageIdentities {
			for _, role := range roles {
				if _, err := c.ExecContext(ctx, "REVOKE ALL ON FUNCTION pg_catalog."+identity+" FROM "+role); err != nil {
					t.Error(err)
				}
			}
			if _, err := c.ExecContext(ctx, "GRANT EXECUTE ON FUNCTION pg_catalog."+identity+" TO PUBLIC"); err != nil {
				t.Error(err)
			}
		}
		if pg {
			if _, err := c.ExecContext(ctx, "UPDATE pg_catalog.pg_proc SET proacl=NULL WHERE oid IN (3577,3578,6344)"); err != nil {
				t.Error(err)
			}
		}
	}
}
