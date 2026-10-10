package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func collationScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend)) {
	t.Helper()
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) { body(t, p, backend) })
	}
}

func collationStep(t *testing.T, p *referencePair, b referenceBackend, s referenceStep) {
	t.Helper()
	referenceRunStep(t, p.ctx, b.db, b.recorder, s)
}

func collationRoutes(t *testing.T, p *referencePair, b referenceBackend, s referenceStep) {
	t.Helper()
	presetStage := s.errorStage
	for _, prepared := range []bool{false, true} {
		for _, nonContext := range []bool{false, true} {
			s.prepared, s.nonContext = prepared, nonContext
			if s.state != "" {
				s.errorStage = presetStage
				if s.errorStage == "" {
					s.errorStage = "execute"
				}
				if presetStage == "" && prepared && strings.HasPrefix(s.query, "SELECT") && s.state != "42P22" {
					s.errorStage = "prepare"
				}
			}
			collationStep(t, p, b, s)
		}
	}
}

func collationExec(t *testing.T, p *referencePair, b referenceBackend, queries ...string) {
	t.Helper()
	for _, q := range queries {
		collationStep(t, p, b, referenceExec(q, 0))
	}
}

func collationError(t *testing.T, p *referencePair, b referenceBackend, q string, d referenceDiagnostic) {
	t.Helper()
	d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
	s := referenceErrorDiagnostic(q, d)
	s.selectRows = strings.HasPrefix(q, "SELECT")
	collationRoutes(t, p, b, s)
}

func collationOID(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var oid int64
	if err := db.QueryRow("SELECT oid FROM pg_catalog.pg_collation WHERE collname=$1", name).Scan(&oid); err != nil {
		t.Fatal(name, err)
	}
	if oid <= 0 || oid > 4294967295 || oid == 950 {
		t.Fatal("invalid independent collation identity", name, oid)
	}
	return oid
}

func collationOwnerOID(t *testing.T, db *sql.DB, name string) int64 {
	t.Helper()
	var owner int64
	if err := db.QueryRow("SELECT collowner FROM pg_catalog.pg_collation WHERE collname=$1", name).Scan(&owner); err != nil {
		t.Fatal(name, err)
	}
	if owner <= 0 || owner > 4294967295 {
		t.Fatal("invalid actual collation owner", name, owner)
	}
	return owner
}

func collationAbsent(t *testing.T, p *referencePair, b referenceBackend, name string) {
	t.Helper()
	collationRoutes(t, p, b, referenceSelect("SELECT count(*) FROM pg_catalog.pg_collation WHERE collname=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, name))
}
