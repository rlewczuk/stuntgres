package reference_test

import (
	"context"
	"errors"
	"testing"
)

func TestReferenceRegclassStorageAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			c, err := backend.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, backend.recorder, s) }
			for _, s := range []referenceStep{referenceExec("CREATE TABLE rr_atomic_target(v INT)", 0), referenceExec("CREATE TABLE rr_atomic_src(id INT,v TEXT)", 0), referenceExec("CREATE TABLE rr_atomic_dst(id INT,r regclass)", 0), referenceExec("INSERT INTO rr_atomic_src VALUES(1,'rr_atomic_target'),(2,'rr_atomic_absent')", 2), referenceExec("INSERT INTO rr_atomic_dst VALUES(0,'0')", 1)} {
				run(s)
			}
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("INSERT INTO rr_atomic_dst SELECT id,v::regclass FROM rr_atomic_src ORDER BY id", referenceDiagnostic{Code: "42P01", Message: `relation "rr_atomic_absent" does not exist`})
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
				run(referenceSelect("SELECT id,r FROM rr_atomic_dst ORDER BY id", []string{"id", "r"}, []string{"INT4", "REGCLASS"}, [][]any{{int64(0), "-"}}))
			}
			run(referenceExec("INSERT INTO rr_atomic_dst VALUES(1,'rr_atomic_target'),(2,'rr_atomic_target')", 2))
			for _, prepared := range []bool{false, true} {
				s := referenceImplicitError("UPDATE rr_atomic_dst SET r=(CASE id WHEN 1 THEN 'rr_atomic_target'::text ELSE 'rr_atomic_absent'::text END)::regclass WHERE id>0", referenceDiagnostic{Code: "42P01", Message: `relation "rr_atomic_absent" does not exist`})
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
				run(referenceSelect("SELECT id,r FROM rr_atomic_dst ORDER BY id", []string{"id", "r"}, []string{"INT4", "REGCLASS"}, [][]any{{int64(0), "-"}, {int64(1), "rr_atomic_target"}, {int64(2), "rr_atomic_target"}}))
			}
			s := referenceExec("UPDATE rr_atomic_dst SET r=$1::regclass WHERE id=1", 1, nil)
			s.prepared = true
			run(s)
			run(referenceSelect("SELECT r FROM rr_atomic_dst WHERE id=1", []string{"r"}, []string{"REGCLASS"}, [][]any{{nil}}))
			if !backend.pg {
				ctx, cancel := context.WithCancel(p.ctx)
				cancel()
				result, failure := c.ExecContext(ctx, "INSERT INTO rr_atomic_dst VALUES(3,'rr_atomic_target')")
				if result != nil || !errors.Is(failure, context.Canceled) {
					t.Fatal(result, failure)
				}
				rows, failure := c.QueryContext(ctx, "SELECT r FROM rr_atomic_dst")
				if rows != nil {
					rows.Close()
				}
				if rows != nil || !errors.Is(failure, context.Canceled) {
					t.Fatal(rows, failure)
				}
				var count int64
				if err = c.QueryRowContext(p.ctx, "SELECT count(*) FROM rr_atomic_dst").Scan(&count); err != nil || count != 3 {
					t.Fatal(count, err)
				}
			}
			run(referenceExec("DROP TABLE rr_atomic_dst", 0))
			run(referenceExec("DROP TABLE rr_atomic_src", 0))
			run(referenceExec("DROP TABLE rr_atomic_target", 0))
		})
	}
}
