package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func pgDescriptionSharedRoleID(t *testing.T, p *fixture.Pair, backend fixture.Backend) int64 {
	t.Helper()
	oid := int64(10)
	if backend.PG {
		if err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_roles WHERE rolname='postgres'").Scan(&oid); err != nil || oid <= 0 {
			t.Fatal("shared role identity", oid, err)
		}
	}
	return oid
}

func pgDescriptionRelationID(t *testing.T, p *fixture.Pair, backend fixture.Backend, name string) int64 {
	t.Helper()
	var oid int64
	if err := backend.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_class WHERE relname=$1", name).Scan(&oid); err != nil || oid <= 0 || oid > 4294967295 {
		t.Fatal("relation identity", name, oid, err)
	}
	return oid
}

func pgDescriptionRelationBag(t *testing.T, p *fixture.Pair, backend fixture.Backend, oid int64, rows [][]any) {
	t.Helper()
	s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=1259", rows, oid)
	fixture.PgDescriptionRows(t, p, backend, s)
	fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(s))
}

func TestReferencePGDescriptionLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprint(prepared), func(t *testing.T) {
			fixture.SchemaReferenceCleanup(t, "pgd_lifecycle")
			fixture.RoleReferenceCleanup(t, []string{"pgd_lifecycle_owner"})
			p := fixture.NewScenario(t)
			for _, backend := range p.Backends() {
				t.Run(backend.Name, func(t *testing.T) {
					run := func(q string) {
						t.Helper()
						s := fixture.Exec(q, 0)
						if prepared {
							s = fixture.Prepared(s)
						}
						fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
					}
					for _, q := range []string{"CREATE ROLE pgd_lifecycle_owner", "CREATE SCHEMA pgd_lifecycle", "CREATE TABLE pgd_life(a INT,b INT,c TEXT)", "COMMENT ON TABLE pgd_life IS 'whole'", "COMMENT ON COLUMN pgd_life.b IS 'middle'", "COMMENT ON COLUMN pgd_life.c IS 'third'", "COMMENT ON SCHEMA pgd_lifecycle IS 'pgd lifecycle schema'"} {
						run(q)
					}
					oid := pgDescriptionRelationID(t, p, backend, "pgd_life")
					whole := []any{oid, int64(1259), int64(0), "whole"}
					third := []any{oid, int64(1259), int64(3), "third"}
					pgDescriptionRelationBag(t, p, backend, oid, [][]any{whole, {oid, int64(1259), int64(2), "middle"}, third})
					for _, q := range []string{"ALTER TABLE pgd_life DROP b", "ALTER TABLE pgd_life RENAME TO pgd_life_renamed", "ALTER TABLE pgd_life_renamed RENAME COLUMN c TO survivor", "ALTER TABLE pgd_life_renamed ALTER COLUMN a TYPE BIGINT", "ALTER TABLE pgd_life_renamed SET SCHEMA pgd_lifecycle"} {
						run(q)
						pgDescriptionRelationBag(t, p, backend, oid, [][]any{whole, third})
					}
					if current := pgDescriptionRelationID(t, p, backend, "pgd_life_renamed"); current != oid {
						t.Fatal("ALTER reminted address", oid, current)
					}
					var namespace int64
					if err := backend.DB.QueryRowContext(p.Ctx, "SELECT objoid FROM pg_catalog.pg_description WHERE classoid=2615 AND description='pgd lifecycle schema'").Scan(&namespace); err != nil {
						t.Fatal(err)
					}
					schemaBag := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=2615", [][]any{{namespace, int64(2615), int64(0), "pgd lifecycle schema"}}, namespace)
					fixture.PgDescriptionRows(t, p, backend, schemaBag)
					run("ALTER SCHEMA pgd_lifecycle OWNER TO pgd_lifecycle_owner")
					fixture.PgDescriptionRows(t, p, backend, schemaBag)
					fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(schemaBag))
					// TABLE OWNER TO is outside the current grammar. Exercise the
					// supported relation-owner authority on a commented sequence.
					run("CREATE SEQUENCE pgd_lifecycle.pgd_life_sequence")
					run("COMMENT ON SEQUENCE pgd_lifecycle.pgd_life_sequence IS 'owner-stable sequence'")
					sequenceOID := pgDescriptionRelationID(t, p, backend, "pgd_life_sequence")
					sequenceBag := [][]any{{sequenceOID, int64(1259), int64(0), "owner-stable sequence"}}
					pgDescriptionRelationBag(t, p, backend, sequenceOID, sequenceBag)
					var oldOwner, newOwner int64
					if err := backend.DB.QueryRowContext(p.Ctx, "SELECT relowner FROM pg_catalog.pg_class WHERE oid=$1", sequenceOID).Scan(&oldOwner); err != nil {
						t.Fatal(err)
					}
					run("ALTER SEQUENCE pgd_lifecycle.pgd_life_sequence OWNER TO pgd_lifecycle_owner")
					if err := backend.DB.QueryRowContext(p.Ctx, "SELECT relowner FROM pg_catalog.pg_class WHERE oid=$1", sequenceOID).Scan(&newOwner); err != nil || newOwner <= 0 || newOwner == oldOwner {
						t.Fatal("sequence ownership did not change", oldOwner, newOwner, err)
					}
					pgDescriptionRelationBag(t, p, backend, sequenceOID, sequenceBag)
					run("DROP SEQUENCE pgd_lifecycle.pgd_life_sequence")
					pgDescriptionRelationBag(t, p, backend, sequenceOID, [][]any{})
					run("ALTER TABLE pgd_lifecycle.pgd_life_renamed ADD fresh TEXT")
					run("COMMENT ON COLUMN pgd_lifecycle.pgd_life_renamed.fresh IS 'fourth'")
					pgDescriptionRelationBag(t, p, backend, oid, [][]any{whole, third, {oid, int64(1259), int64(4), "fourth"}})
					run("ALTER TABLE pgd_lifecycle.pgd_life_renamed DROP fresh")
					pgDescriptionRelationBag(t, p, backend, oid, [][]any{whole, third})
					run("ALTER TABLE pgd_lifecycle.pgd_life_renamed DROP survivor")
					pgDescriptionRelationBag(t, p, backend, oid, [][]any{whole})
					want := fixture.ControlsReferenceDiagnostic("2BP01", "cannot drop schema pgd_lifecycle because other objects depend on it")
					want.Detail = "table pgd_lifecycle.pgd_life_renamed depends on schema pgd_lifecycle"
					want.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
					failure := fixture.ErrorDiagnostic("DROP SCHEMA pgd_lifecycle RESTRICT", want)
					failure.ErrorStage = "execute"
					if prepared {
						failure = fixture.Prepared(failure)
					}
					fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, failure)
					pgDescriptionRelationBag(t, p, backend, oid, [][]any{whole})
					fixture.PgDescriptionRows(t, p, backend, schemaBag)
					fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(schemaBag))
					run("DROP SCHEMA pgd_lifecycle CASCADE")
					pgDescriptionRelationBag(t, p, backend, oid, [][]any{})
					s := pgDescriptionSelect("SELECT * FROM pg_catalog.pg_description WHERE objoid=$1 AND classoid=2615", [][]any{}, namespace)
					fixture.PgDescriptionRows(t, p, backend, s)
					fixture.PgDescriptionRows(t, p, backend, fixture.Prepared(s))
					run("CREATE TABLE pgd_life_renamed(a INT)")
					fresh := pgDescriptionRelationID(t, p, backend, "pgd_life_renamed")
					if fresh == oid {
						t.Fatal("recreation reused old address")
					}
					pgDescriptionRelationBag(t, p, backend, fresh, [][]any{})
					run("COMMENT ON TABLE pgd_life_renamed IS 'recreated'")
					pgDescriptionRelationBag(t, p, backend, fresh, [][]any{{fresh, int64(1259), int64(0), "recreated"}})
					run("DROP TABLE pgd_life_renamed")
					pgDescriptionRelationBag(t, p, backend, fresh, [][]any{})
				})
			}
		})
	}
}
