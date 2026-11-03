package reference_test

import (
	"database/sql"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const statSubscriptionSQL = "SELECT * FROM pg_catalog.pg_stat_subscription"

var statSubscriptionNames = []string{"subid", "subname", "worker_type", "pid", "leader_pid", "relid", "received_lsn", "last_msg_send_time", "last_msg_receipt_time", "latest_end_lsn", "latest_end_time"}
var statSubscriptionTypes = []string{"OID", "NAME", "TEXT", "INT4", "INT4", "OID", "PG_LSN", "TIMESTAMPTZ", "TIMESTAMPTZ", "PG_LSN", "TIMESTAMPTZ"}
var statSubscriptionTypeIDs = []int64{26, 19, 25, 23, 23, 26, 3220, 1184, 1184, 3220, 1184}

func statSubscriptionScenario(t *testing.T, names []string, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	clean := fixture.SubscriptionReferenceCleanup(t, names)
	p := fixture.NewScenario(t)
	clean()
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			body(t, p, b, c)
		})
	}
}

func statSubscriptionExec(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Exec(query, 0))
}

func statSubscriptionCreate(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, name string) {
	t.Helper()
	statSubscriptionExec(t, p, b, e, "CREATE SUBSCRIPTION "+name+" CONNECTION '' PUBLICATION p WITH (connect=false,slot_name=NONE)")
}

func statSubscriptionID(t *testing.T, p *fixture.Pair, e fixture.Executor, name string) int64 {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, "SELECT oid::bigint FROM pg_subscription WHERE subname=$1 AND subdbid=(SELECT oid FROM pg_database WHERE datname=current_database())", name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var id int64
	if !rows.Next() {
		t.Fatal("missing actual subscription identity", name, rows.Err())
	}
	if err := rows.Scan(&id); err != nil || id <= 0 {
		t.Fatal("positive OID", id, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("one actual subscription", name, rows.Err())
	}
	return id
}

func statSubscriptionHeld(t *testing.T, p *fixture.Pair, c *sql.Conn, query string) fixture.HeldStatement {
	t.Helper()
	stmt, err := c.PrepareContext(p.Ctx, query)
	if err != nil {
		t.Fatal(query, err)
	}
	return fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
}

func statSubscriptionMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, statSubscriptionNames) {
		t.Fatal("eleven released column labels", names, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 11 {
		t.Fatal("eleven typed descriptors", columns, err)
	}
	for i, c := range columns {
		scan := reflect.TypeFor[string]()
		switch statSubscriptionTypes[i] {
		case "OID", "INT4":
			scan = reflect.TypeFor[int64]()
		case "TIMESTAMPTZ":
			scan = reflect.TypeFor[time.Time]()
		}
		if pg {
			switch statSubscriptionTypes[i] {
			case "OID", "NAME", "PG_LSN":
				scan = reflect.TypeFor[any]()
			case "INT4":
				scan = reflect.TypeFor[int32]()
			}
		}
		if c.Name() != names[i] || c.DatabaseTypeName() != statSubscriptionTypes[i] || c.ScanType() != scan {
			t.Fatalf("column %d: %s/%s/%v want %s/%s/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), names[i], statSubscriptionTypes[i], scan)
		}
		if nullable, known := c.Nullable(); nullable != !pg || known != !pg {
			t.Fatalf("column %d nullable=%t/%t", i, nullable, known)
		}
		length, lengthKnown := int64(0), false
		if statSubscriptionTypes[i] == "TEXT" {
			length, lengthKnown = math.MaxInt64, true
		}
		if n, known := c.Length(); n != length || known != lengthKnown {
			t.Fatalf("column %d length=%d/%t want %d/%t", i, n, known, length, lengthKnown)
		}
		if precision, scale, known := c.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatalf("column %d decimal=%d/%d/%t", i, precision, scale, known)
		}
	}
}

func statSubscriptionRow(id int64, name string) []any {
	return []any{id, name, nil, nil, nil, nil, nil, nil, nil, nil, nil}
}

func statSubscriptionRead(t *testing.T, rows *sql.Rows, pg bool, want [][]any) {
	t.Helper()
	defer rows.Close()
	statSubscriptionMetadata(t, rows, pg)
	var got [][]any
	for rows.Next() {
		row, destinations := make([]any, 11), make([]any, 11)
		for i := range destinations {
			destinations[i] = &row[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		for i, cell := range row {
			row[i] = fixture.OidReferenceValue(t, pg, statSubscriptionTypes[i], cell)
		}
		got = append(got, row)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if _, matches := fixture.CompareRows(got, want, true); !matches {
		t.Fatal("exact identity and nine NULLs (unordered bag)", got, want)
	}
}

func statSubscriptionCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, want [][]any, args ...any) {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, query, args...)
	if err != nil {
		t.Fatal(query, err)
	}
	statSubscriptionRead(t, rows, b.PG, want)
}

func statSubscriptionMutation(operation string) fixture.Diagnostic {
	action := map[string]string{"INSERT": "inserting into", "UPDATE": "updating", "DELETE": "deleting from"}[operation]
	message := map[string]string{"INSERT": `cannot insert into view "pg_stat_subscription"`, "UPDATE": `cannot update view "pg_stat_subscription"`, "DELETE": `cannot delete from view "pg_stat_subscription"`}[operation]
	want := fixture.ControlsReferenceDiagnostic("55000", message)
	want.Detail = "Views that do not select from a single table or view are not automatically updatable."
	want.Hint = "To enable " + action + " the view, provide an INSTEAD OF " + operation + " trigger or an unconditional ON " + operation + " DO INSTEAD rule."
	return want
}

func statSubscriptionSentinel(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, e, b.Recorder, fixture.Select("SELECT v FROM stat_subscription_sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(37)}}))
}

func statSubscriptionProjection() string {
	return "SELECT " + strings.Join(statSubscriptionNames, ",") + " FROM pg_catalog.pg_stat_subscription"
}
