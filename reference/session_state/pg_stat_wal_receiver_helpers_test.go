package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const walReceiverSQL = "SELECT * FROM pg_catalog.pg_stat_wal_receiver"

var walReceiverColumnNames = []string{"pid", "status", "receive_start_lsn", "receive_start_tli", "written_lsn", "flushed_lsn", "received_tli", "last_msg_send_time", "last_msg_receipt_time", "latest_end_lsn", "latest_end_time", "slot_name", "sender_host", "sender_port", "conninfo"}
var walReceiverTypes = []string{"INT4", "TEXT", "PG_LSN", "INT4", "PG_LSN", "PG_LSN", "INT4", "TIMESTAMPTZ", "TIMESTAMPTZ", "PG_LSN", "TIMESTAMPTZ", "TEXT", "TEXT", "INT4", "TEXT"}
var walReceiverTypeOIDs = []int64{23, 25, 3220, 23, 3220, 3220, 23, 1184, 1184, 3220, 1184, 25, 25, 23, 25}

func walReceiverMetadata(t *testing.T, rows *sql.Rows, pg bool, indices []int) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || len(names) != len(indices) {
		t.Fatal("walReceiver column names", names, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != len(indices) {
		t.Fatal("walReceiver descriptors", columns, err)
	}
	walReceiverDescriptors(t, names, columns, pg, indices)
}

func walReceiverDescriptors(t *testing.T, names []string, columns []*sql.ColumnType, pg bool, indices []int) {
	t.Helper()
	if len(names) != len(indices) || len(columns) != len(indices) {
		t.Fatal("receiver descriptor width", names, columns)
	}
	for j, i := range indices {
		c := columns[j]
		if names[j] != walReceiverColumnNames[i] || c.Name() != walReceiverColumnNames[i] || c.DatabaseTypeName() != walReceiverTypes[i] {
			t.Fatal("PG18 column contract", j, names[j], c.Name(), c.DatabaseTypeName())
		}
		scan := reflect.TypeFor[string]()
		switch walReceiverTypes[i] {
		case "INT4":
			scan = reflect.TypeFor[int64]()
			if pg {
				scan = reflect.TypeFor[int32]()
			}
		case "TIMESTAMPTZ":
			scan = reflect.TypeFor[time.Time]()
		}
		if pg && walReceiverTypes[i] != "INT4" && walReceiverTypes[i] != "TIMESTAMPTZ" && walReceiverTypes[i] != "TEXT" {
			scan = reflect.TypeFor[any]()
		}
		if c.ScanType() != scan {
			t.Fatal("adapter scan type", i, c.ScanType(), scan)
		}
		if nullable, known := c.Nullable(); nullable != !pg || known != !pg {
			t.Fatal("nullable view field", i, nullable, known)
		}
		wantLength, wantLengthKnown := int64(0), false
		if walReceiverTypes[i] == "TEXT" {
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

// Retain the public descriptors themselves across EOF/Close, not a request to
// read metadata from an already closed database/sql cursor.
func walReceiverFinishEmpty(t *testing.T, rows *sql.Rows, pg bool, indices []int) {
	t.Helper()
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	walReceiverDescriptors(t, names, columns, pg, indices)
	if rows.Next() || rows.Err() != nil {
		t.Fatal("receiver must have clean empty EOF", rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	walReceiverDescriptors(t, names, columns, pg, indices)
}

func walReceiverStep(t *testing.T, b fixture.Backend, query string, indices []int, args ...any) fixture.Step {
	t.Helper()
	names, types := make([]string, len(indices)), make([]string, len(indices))
	for j, i := range indices {
		names[j], types[j] = walReceiverColumnNames[i], walReceiverTypes[i]
	}
	step := fixture.Select(query, names, types, [][]any{}, args...)
	step.Bag = true
	step.CheckMetadata = func(t *testing.T, rows *sql.Rows) { walReceiverMetadata(t, rows, b.PG, indices) }
	return step
}

func walReceiverAllColumns() []int {
	return []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14}
}

func walReceiverEmpty(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, walReceiverStep(t, b, walReceiverSQL, walReceiverAllColumns()))
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT count(*) FROM pg_catalog.pg_stat_wal_receiver", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func walReceiverHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
}

func walReceiverSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT v FROM wal_receiver_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
}

func walReceiverMutationDiagnostic(op string) fixture.Diagnostic {
	action := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[op]
	verb := map[string]string{"INSERT": "insert into", "UPDATE": "update", "DELETE": "delete from"}[op]
	return fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "55000", Message: "cannot " + verb + " view \"pg_stat_wal_receiver\"", Detail: "Views that do not select from a single table or view are not automatically updatable.", Hint: "To enable " + action + " the view, provide an INSTEAD OF " + op + " trigger or an unconditional ON " + op + " DO INSTEAD rule."}
}
