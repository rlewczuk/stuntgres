package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const replicationSQL = "SELECT * FROM pg_catalog.pg_stat_replication"

var replicationColumnNames = []string{"pid", "usesysid", "usename", "application_name", "client_addr", "client_hostname", "client_port", "backend_start", "backend_xmin", "state", "sent_lsn", "write_lsn", "flush_lsn", "replay_lsn", "write_lag", "flush_lag", "replay_lag", "sync_priority", "sync_state", "reply_time"}
var replicationTypes = []string{"INT4", "OID", "NAME", "TEXT", "INET", "TEXT", "INT4", "TIMESTAMPTZ", "XID", "TEXT", "PG_LSN", "PG_LSN", "PG_LSN", "PG_LSN", "INTERVAL", "INTERVAL", "INTERVAL", "INT4", "TEXT", "TIMESTAMPTZ"}
var replicationTypeOIDs = []int64{23, 26, 19, 25, 869, 25, 23, 1184, 28, 25, 3220, 3220, 3220, 3220, 1186, 1186, 1186, 23, 25, 1184}

func replicationMetadata(t *testing.T, rows *sql.Rows, pg bool, indices []int) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || len(names) != len(indices) {
		t.Fatal("replication column names", names, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != len(indices) {
		t.Fatal("replication descriptors", columns, err)
	}
	for j, i := range indices {
		c := columns[j]
		if names[j] != replicationColumnNames[i] || c.Name() != replicationColumnNames[i] || c.DatabaseTypeName() != replicationTypes[i] {
			t.Fatal("PG18 column contract", j, names[j], c.Name(), c.DatabaseTypeName())
		}
		scan := reflect.TypeFor[string]()
		switch replicationTypes[i] {
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		case "OID":
			scan = reflect.TypeFor[int64]()
		case "XID":
			scan = reflect.TypeFor[[]byte]()
		case "TIMESTAMPTZ":
			scan = reflect.TypeFor[time.Time]()
		}
		if pg && replicationTypes[i] != "INT4" && replicationTypes[i] != "TIMESTAMPTZ" && replicationTypes[i] != "TEXT" {
			scan = reflect.TypeFor[any]()
		}
		if c.ScanType() != scan {
			t.Fatal("adapter scan type", i, c.ScanType(), scan)
		}
		if nullable, known := c.Nullable(); nullable != !pg || known != !pg {
			t.Fatal("nullable view field", i, nullable, known)
		}
		wantLength, wantLengthKnown := int64(0), false
		if replicationTypes[i] == "TEXT" {
			wantLength, wantLengthKnown = math.MaxInt64, true
		}
		if n, known := c.Length(); n != wantLength || known != wantLengthKnown {
			t.Fatal("adapter length", i, n, known)
		}
		if p, s, known := c.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatal("undeclared precision", i, p, s, known)
		}
	}
}

func replicationStep(t *testing.T, b fixture.Backend, query string, indices []int, args ...any) fixture.Step {
	t.Helper()
	names, types := make([]string, len(indices)), make([]string, len(indices))
	for j, i := range indices {
		names[j], types[j] = replicationColumnNames[i], replicationTypes[i]
	}
	step := fixture.Select(query, names, types, [][]any{}, args...)
	step.Bag = true
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { replicationMetadata(t, rows, b.PG, indices) }
	return step
}

func replicationAllColumns() []int {
	return []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19}
}

func replicationEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, replicationStep(t, b, replicationSQL, replicationAllColumns()))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT count(*) FROM pg_catalog.pg_stat_replication", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func replicationHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
}

func replicationSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM replication_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
}

func replicationMutationDiagnostic(op string) fixture.Diagnostic {
	action := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[op]
	verb := map[string]string{"INSERT": "insert into", "UPDATE": "update", "DELETE": "delete from"}[op]
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "cannot " + verb + " view \"pg_stat_replication\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + action + " the view, provide an INSTEAD OF " + op + " trigger or an unconditional ON " + op + " DO INSTEAD rule."}
}
