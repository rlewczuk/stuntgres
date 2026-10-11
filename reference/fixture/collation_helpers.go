package fixture

import (
	"database/sql"
	"strings"
	"testing"
)

func CollationScenario(t *testing.T, body func(*testing.T, *Pair, Backend)) {
	t.Helper()
	p := NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) { body(t, p, backend) })
	}
}

func CollationStep(t *testing.T, p *Pair, b Backend, s Step) {
	t.Helper()
	RunStep(t, p.Ctx, b.DB, b.Recorder, s)
}

func CollationRoutes(t *testing.T, p *Pair, b Backend, s Step) {
	t.Helper()
	presetStage := s.ErrorStage
	for _, prepared := range []bool{false, true} {
		for _, nonContext := range []bool{false, true} {
			s.Prepared, s.NonContext = prepared, nonContext
			if s.State != "" {
				s.ErrorStage = presetStage
				if s.ErrorStage == "" {
					s.ErrorStage = "execute"
				}
				if presetStage == "" && prepared && strings.HasPrefix(s.Query, "SELECT") && s.State != "42P22" {
					s.ErrorStage = "prepare"
				}
			}
			CollationStep(t, p, b, s)
		}
	}
}

func CollationExec(t *testing.T, p *Pair, b Backend, queries ...string) {
	t.Helper()
	for _, q := range queries {
		CollationStep(t, p, b, Exec(q, 0))
	}
}

func CollationError(t *testing.T, p *Pair, b Backend, q string, d Diagnostic) {
	t.Helper()
	d.Severity, d.SeverityUnlocalized = "ERROR", "ERROR"
	s := ErrorDiagnostic(q, d)
	s.SelectRows = strings.HasPrefix(q, "SELECT")
	CollationRoutes(t, p, b, s)
}

func CollationOID(t *testing.T, db *sql.DB, name string) int64 {
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

func CollationOwnerOID(t *testing.T, db *sql.DB, name string) int64 {
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

func CollationAbsent(t *testing.T, p *Pair, b Backend, name string) {
	t.Helper()
	CollationRoutes(t, p, b, Select("SELECT count(*) FROM pg_catalog.pg_collation WHERE collname=$1", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, name))
}
