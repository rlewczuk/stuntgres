//go:build reference

package reference_test

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"reflect"
	"testing"
)

type referenceByteaValuer struct {
	raw   driver.Value
	err   error
	calls *int
}

func (v referenceByteaValuer) Value() (driver.Value, error) { (*v.calls)++; return v.raw, v.err }
func TestReferenceByteaValuerAndInference(t *testing.T) {
	p := newReferencePair(t)
	for _, db := range []*sql.DB{p.memory, p.postgres} {
		if _, err := db.ExecContext(p.ctx, "CREATE TABLE t(id INTEGER,v BYTEA)"); err != nil {
			t.Fatal(err)
		}
		for i, tc := range []struct {
			raw  driver.Value
			want []byte
		}{{`\x41`, []byte{92, 120, 52, 49}}, {[]byte{0, 128, 255}, []byte{0, 128, 255}}} {
			for _, prepared := range []bool{false, true} {
				calls := 0
				v := referenceByteaValuer{tc.raw, nil, &calls}
				var err error
				if prepared {
					s, e := db.PrepareContext(p.ctx, "INSERT INTO t VALUES($1,$2)")
					if e != nil {
						t.Fatal(e)
					}
					_, err = s.ExecContext(p.ctx, i, v)
					s.Close()
				} else {
					_, err = db.ExecContext(p.ctx, "INSERT INTO t VALUES($1,$2)", i, v)
				}
				if err != nil || calls != 1 {
					t.Fatal(err, calls)
				}
			}
			rows, err := db.QueryContext(p.ctx, "SELECT v FROM t WHERE id=$1", i)
			if err != nil {
				t.Fatal(err)
			}
			n := 0
			for rows.Next() {
				var got any
				if err := rows.Scan(&got); err != nil || !reflect.DeepEqual(got, tc.want) {
					t.Fatal(got, tc.want, err)
				}
				n++
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
			if n != 2 {
				t.Fatal(n)
			}
		}
		sentinel := errors.New("valuer failure")
		calls := 0
		_, err := db.ExecContext(p.ctx, "INSERT INTO t VALUES(9,$1)", referenceByteaValuer{nil, sentinel, &calls})
		if !errors.Is(err, sentinel) || calls != 1 {
			t.Fatal(err, calls)
		}
		var state interface{ SQLState() string }
		if errors.As(err, &state) {
			t.Fatal("ordinary Valuer error became SQL error")
		}
		want := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "bytea versus text", Position: "8"}
		rows, err := db.QueryContext(p.ctx, "SELECT $1 FROM t WHERE v=$1", []byte{65})
		if rows != nil {
			rows.Close()
		}
		referenceAssertDiagnostic(t, err, &want)
		s, err := db.PrepareContext(p.ctx, "SELECT $1 FROM t WHERE v=$1")
		if s != nil {
			s.Close()
		}
		referenceAssertDiagnostic(t, err, &want)
	}
}
