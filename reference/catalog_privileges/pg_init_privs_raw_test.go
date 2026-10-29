package reference_test

import (
	"database/sql"
	"github.com/lib/pq"
	"reflect"
	"sort"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func ipRawSelf(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	owner := ipOwner(t, p, b)
	var ownerName string
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT rolname FROM pg_roles WHERE oid=$1", owner).Scan(&ownerName); err != nil {
		t.Fatal(err)
	}
	q := "SELECT * FROM pg_init_privs WHERE classoid=1259 AND objoid=3394 AND objsubid=0"
	stmt, err := b.DB.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, prepared := range []bool{false, true} {
		var rows *sql.Rows
		if prepared {
			rows, err = stmt.QueryContext(p.Ctx)
		} else {
			rows, err = b.DB.QueryContext(p.Ctx, q)
		}
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer rows.Close()
			types, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			ipMetadata(t, types, b.PG)
			if !rows.Next() {
				t.Fatal("missing self snapshot", rows.Err())
			}
			values := make([]any, 5)
			targets := make([]any, 5)
			for i := range values {
				targets[i] = &values[i]
			}
			if err := rows.Scan(targets...); err != nil {
				t.Fatal(err)
			}
			for i, v := range values {
				fixture.AssertRawValue(t, b.PG, ipTypes[i], v)
			}
			for i, want := range []int64{3394, 1259, 0} {
				var got int64
				switch v := values[i].(type) {
				case int64:
					got = v
				case []byte:
					got, err = strconv.ParseInt(string(v), 10, 64)
					if err != nil {
						t.Fatal(err)
					}
				}
				if got != want {
					t.Fatal("raw address", i, got, want)
				}
			}
			if values[3] != "i" {
				t.Fatal("initial kind", values[3])
			}
			var entries pq.StringArray
			if err := entries.Scan(values[4]); err != nil {
				t.Fatal(err)
			}
			sort.Strings(entries)
			want := []string{"=r/" + ownerName, ownerName + "=arwdDxtm/" + ownerName}
			sort.Strings(want)
			if !reflect.DeepEqual([]string(entries), want) {
				t.Fatal("independent self ACL entry bag", entries, want)
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("self EOF", rows.Err())
			}
		}()
	}
}
