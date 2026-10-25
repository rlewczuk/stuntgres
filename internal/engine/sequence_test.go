package engine

import (
	"database/sql/driver"
	"reflect"
	"testing"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/sequence"
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

// currvalIs reports whether a session memory holds currval want.
func currvalIs(m *sequence.Memory, want int64) bool {
	v, ok := m.Current()
	return ok && v == want
}

// currvalValid reports whether a session memory holds a currval.
func currvalValid(m *sequence.Memory) bool {
	_, ok := m.Current()
	return ok
}

// currvalValue is the remembered currval value, valid or not.
func currvalValue(m *sequence.Memory) int64 {
	v, _ := m.Current()
	return v
}

// cachedSequencePathIs reports whether the session caches exactly key.
func cachedSequencePathIs(s *session.Session, key sequence.PathKey) bool {
	cached, ok := core.PayloadOf(s).Sequences.CachedPathKey()
	return ok && cached == key
}
