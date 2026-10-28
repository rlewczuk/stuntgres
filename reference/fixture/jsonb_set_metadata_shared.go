package fixture

import (
	"database/sql"
	"reflect"
	"testing"
)

func JSONBSetMetadataStep(s Step, pg bool) Step {
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { JSONBObjectMetadata(t, r, pg, "changed") }
	s.CheckScan = func(t *testing.T, r *sql.Rows, i int) {
		want := s.Rows[i][0]
		var raw any
		if err := r.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		AssertRawValue(t, pg, "JSONB", raw)
		if !reflect.DeepEqual(Normalize(raw), want) {
			t.Fatalf("raw=%#v want=%#v", raw, want)
		}
		var n sql.NullString
		if err := r.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n.Valid != (want != nil) || (n.Valid && n.String != want) {
			t.Fatalf("NullString=%#v want=%#v", n, want)
		}
		var data []byte
		if err := r.Scan(&data); err != nil {
			t.Fatal(err)
		}
		if want == nil {
			if data != nil {
				t.Fatal(data)
			}
			return
		}
		if string(data) != want {
			t.Fatalf("bytes=%q want=%q", data, want)
		}
		data[0] = 'x'
		var text string
		if err := r.Scan(&text); err != nil || text != want {
			t.Fatalf("detached text=%q want=%q err=%v", text, want, err)
		}
	}
	return s
}
