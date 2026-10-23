package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func snapshotRemedyScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

// Fully consume on the querying goroutine, including pq's streaming errors,
// before publishing a worker result. No testing.T calls escape to that worker.
type snapshotRemedyRows struct {
	columns []string
	types   []string
	values  [][]any
	err     error
}

func snapshotRemedyRead(rows *sql.Rows, err error) (out snapshotRemedyRows) {
	if err != nil {
		out.err = err
		return
	}
	defer func() {
		if err := rows.Close(); out.err == nil {
			out.err = err
		}
	}()
	out.columns, out.err = rows.Columns()
	if out.err != nil {
		return
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		out.err = err
		return
	}
	for _, typ := range types {
		out.types = append(out.types, typ.DatabaseTypeName())
	}
	for rows.Next() {
		values := make([]any, len(out.columns))
		dest := make([]any, len(values))
		for i := range dest {
			dest[i] = &values[i]
		}
		if out.err = rows.Scan(dest...); out.err != nil {
			return
		}
		for i, value := range values {
			// Preserve the raw pg_snapshot contract before normalizing bytes.
			if out.types[i] == "PG_SNAPSHOT" || out.types[i] == "" {
				if _, ok := value.([]byte); !ok {
					out.err = fmt.Errorf("snapshot column %d has raw type %T, want []byte", i, value)
					return
				}
			}
			values[i] = fixture.Normalize(value)
		}
		out.values = append(out.values, values)
	}
	out.err = rows.Err()
	return
}

func snapshotRemedyShape(t *testing.T, out snapshotRemedyRows, columns, types []string, count int) {
	t.Helper()
	if out.err != nil {
		t.Fatal(out.err)
	}
	if !reflect.DeepEqual(out.columns, columns) || !reflect.DeepEqual(out.types, types) || len(out.values) != count {
		t.Fatalf("rows columns=%v types=%v values=%v, want columns=%v types=%v count=%d", out.columns, out.types, out.values, columns, types, count)
	}
}

func snapshotRemedyPeer(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s, id string, visible bool) {
	t.Helper()
	snapshotPeerPresent(t, s, id, !visible)
	if snapshotNumber(t, id) >= snapshotParse(t, s).max {
		t.Fatalf("peer %s not below snapshot xmax: %s", id, s)
	}
	snapshotVisible(t, p, b, c, id, s, visible)
}
