package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

const statSubscriptionStatsSQL = "SELECT * FROM pg_catalog.pg_stat_subscription_stats"

var statSubscriptionStatsNames = []string{"subid", "subname", "apply_error_count", "sync_error_count", "confl_insert_exists", "confl_update_origin_differs", "confl_update_exists", "confl_update_missing", "confl_delete_origin_differs", "confl_delete_missing", "confl_multiple_unique_conflicts", "stats_reset"}
var statSubscriptionStatsTypes = []string{"OID", "NAME", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "TIMESTAMPTZ"}
var statSubscriptionStatsTypeIDs = []int64{26, 19, 20, 20, 20, 20, 20, 20, 20, 20, 20, 1184}

func statSubscriptionStatsRow(id int64, name string, reset ...time.Time) []any {
	row := []any{id, name, int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), int64(0), nil}
	if len(reset) != 0 && !reset[0].IsZero() {
		row[11] = reset[0].UTC()
	}
	return row
}

func statSubscriptionStatsMetadata(t *testing.T, rows *sql.Rows, pg bool) {
	t.Helper()
	names, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(names, statSubscriptionStatsNames) {
		t.Fatal("twelve released labels", names, err)
	}
	columns, err := rows.ColumnTypes()
	if err != nil || len(columns) != 12 {
		t.Fatal("twelve descriptors", columns, err)
	}
	for i, c := range columns {
		scan := reflect.TypeFor[int64]()
		if i == 1 {
			scan = reflect.TypeFor[string]()
		}
		if i == 11 {
			scan = reflect.TypeFor[time.Time]()
		}
		if pg && i < 2 {
			scan = reflect.TypeFor[any]()
		}
		if c.Name() != names[i] || c.DatabaseTypeName() != statSubscriptionStatsTypes[i] || c.ScanType() != scan {
			t.Fatalf("column %d: %s/%s/%v want %s/%s/%v", i, c.Name(), c.DatabaseTypeName(), c.ScanType(), names[i], statSubscriptionStatsTypes[i], scan)
		}
		if nullable, known := c.Nullable(); nullable != !pg || known != !pg {
			t.Fatal("nullable view descriptor", i, nullable, known)
		}
		if n, known := c.Length(); n != 0 || known {
			t.Fatal("unavailable length", i, n, known)
		}
		if precision, scale, known := c.DecimalSize(); precision != 0 || scale != 0 || known {
			t.Fatal("unavailable decimal", i, precision, scale, known)
		}
	}
}

func statSubscriptionStatsRead(t *testing.T, rows *sql.Rows, pg bool, want [][]any) {
	t.Helper()
	defer rows.Close()
	statSubscriptionStatsMetadata(t, rows, pg)
	var got [][]any
	for rows.Next() {
		row, destinations := make([]any, 12), make([]any, 12)
		for i := range row {
			destinations[i] = &row[i]
		}
		if err := rows.Scan(destinations...); err != nil {
			t.Fatal(err)
		}
		// Assert raw transport before normalization; OID is adapter-specific.
		if pg {
			switch row[0].(type) {
			case []byte, string:
			default:
				t.Fatalf("pq OID transport %T", row[0])
			}
		} else if _, ok := row[0].(int64); !ok {
			t.Fatalf("native OID transport %T", row[0])
		}
		for i := 2; i <= 10; i++ {
			if _, ok := row[i].(int64); !ok {
				t.Fatalf("counter %d transport %T", i, row[i])
			}
		}
		for i, cell := range row {
			row[i] = fixture.OidReferenceValue(t, pg, statSubscriptionStatsTypes[i], cell)
		}
		got = append(got, row)
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if _, matches := fixture.CompareRows(got, want, true); !matches {
		t.Fatal("exact cumulative row bag", got, want)
	}
}

func statSubscriptionStatsCheck(t *testing.T, p *fixture.Pair, b fixture.Backend, e fixture.Executor, query string, want [][]any, args ...any) {
	t.Helper()
	rows, err := e.QueryContext(p.Ctx, query, args...)
	if err != nil {
		t.Fatal(query, err)
	}
	statSubscriptionStatsRead(t, rows, b.PG, want)
}

func statSubscriptionStatsProjection() string {
	return "SELECT " + strings.Join(statSubscriptionStatsNames, ",") + " FROM pg_catalog.pg_stat_subscription_stats"
}

func statSubscriptionStatsMutation(operation string) fixture.Diagnostic {
	want := statSubscriptionMutation(operation)
	want.Message = strings.ReplaceAll(want.Message, "pg_stat_subscription", "pg_stat_subscription_stats")
	return want
}

// Both bounds come from the resetting backend, not the client's wall clock.
func statSubscriptionStatsReset(t *testing.T, p *fixture.Pair, e fixture.Executor, id any) time.Time {
	t.Helper()
	var lower, upper time.Time
	rows, err := e.QueryContext(p.Ctx, "SELECT clock_timestamp()")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	if err := rows.Scan(&lower); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	rows, err = e.QueryContext(p.Ctx, "SELECT pg_stat_reset_subscription_stats($1::oid)", id)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	if rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	rows.Close()
	var reset time.Time
	rows, err = e.QueryContext(p.Ctx, "SELECT stats_reset,clock_timestamp() FROM pg_stat_subscription_stats WHERE subid=$1::oid", id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("reset identity missing", rows.Err())
	}
	if err := rows.Scan(&reset, &upper); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil || reset.Before(lower) || reset.After(upper) {
		t.Fatal("single genuine bounded reset", reset, lower, upper, rows.Err())
	}
	return reset.UTC()
}
