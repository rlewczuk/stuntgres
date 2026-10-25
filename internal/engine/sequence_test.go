package engine

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine/session"
)

func sequenceScalar(t *testing.T, s *session.Session, q string, want int64) {
	t.Helper()
	r := sessionSQL(t, s, q)
	defer r.Rows.Close()
	if got := tuples(r); !reflect.DeepEqual(got, [][]driver.Value{{want}}) {
		t.Fatalf("%s got %v want %d", q, got, want)
	}
}
