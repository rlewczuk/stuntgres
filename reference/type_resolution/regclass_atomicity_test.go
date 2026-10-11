package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegclassStorageAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c, err := backend.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, backend.Recorder, s) }
			for _, s := range []fixture.Step{fixture.Exec("CREATE TABLE rr_atomic_target(v INT)", 0), fixture.Exec("CREATE TABLE rr_atomic_src(id INT,v TEXT)", 0), fixture.Exec("CREATE TABLE rr_atomic_dst(id INT,r regclass)", 0), fixture.Exec("INSERT INTO rr_atomic_src VALUES(1,'rr_atomic_target'),(2,'rr_atomic_absent')", 2), fixture.Exec("INSERT INTO rr_atomic_dst VALUES(0,'0')", 1)} {
				run(s)
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("INSERT INTO rr_atomic_dst SELECT id,v::regclass FROM rr_atomic_src ORDER BY id", fixture.Diagnostic{Code: "42P01", Message: `relation "rr_atomic_absent" does not exist`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT id,r FROM rr_atomic_dst ORDER BY id", []string{"id", "r"}, []string{"INT4", "REGCLASS"}, [][]any{{int64(0), "-"}}))
			}
			run(fixture.Exec("INSERT INTO rr_atomic_dst VALUES(1,'rr_atomic_target'),(2,'rr_atomic_target')", 2))
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("UPDATE rr_atomic_dst SET r=(CASE id WHEN 1 THEN 'rr_atomic_target'::text ELSE 'rr_atomic_absent'::text END)::regclass WHERE id>0", fixture.Diagnostic{Code: "42P01", Message: `relation "rr_atomic_absent" does not exist`})
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
				run(fixture.Select("SELECT id,r FROM rr_atomic_dst ORDER BY id", []string{"id", "r"}, []string{"INT4", "REGCLASS"}, [][]any{{int64(0), "-"}, {int64(1), "rr_atomic_target"}, {int64(2), "rr_atomic_target"}}))
			}
			s := fixture.Exec("UPDATE rr_atomic_dst SET r=$1::regclass WHERE id=1", 1, nil)
			s.Prepared = true
			run(s)
			run(fixture.Select("SELECT r FROM rr_atomic_dst WHERE id=1", []string{"r"}, []string{"REGCLASS"}, [][]any{{nil}}))
			if !backend.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
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
				if err = c.QueryRowContext(p.Ctx, "SELECT count(*) FROM rr_atomic_dst").Scan(&count); err != nil || count != 3 {
					t.Fatal(count, err)
				}
			}
			run(fixture.Exec("DROP TABLE rr_atomic_dst", 0))
			run(fixture.Exec("DROP TABLE rr_atomic_src", 0))
			run(fixture.Exec("DROP TABLE rr_atomic_target", 0))
		})
	}
}
