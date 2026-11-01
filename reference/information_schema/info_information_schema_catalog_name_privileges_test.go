package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func iscnPrivileges(t *testing.T, p *fixture.Pair, c *sql.Conn, pool *sql.DB, r *fixture.Recorder) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
	run(fixture.Exec("CREATE ROLE iscn_reader", 0))
	one := iscnStep("catalog_name", "", true)
	empty := iscnStep("catalog_name", "FALSE", false)
	held := iscnHold(t, p, c, one.Query)
	heldFalse := iscnHold(t, p, c, empty.Query)
	admin, err := pool.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	// Restore unconditionally while the independent administrator is still owned.
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "GRANT SELECT ON "+iscnRelation+" TO PUBLIC"); err != nil {
			t.Error(err)
		}
	}()
	run(fixture.Exec("SET SESSION AUTHORIZATION iscn_reader", 0))
	iscnRoutes(t, p, c, r, held, one)
	iscnRoutes(t, p, c, r, heldFalse, empty)
	fixture.RunStep(t, p.Ctx, admin, r, fixture.Exec("REVOKE SELECT ON "+iscnRelation+" FROM PUBLIC,iscn_reader", 0))
	for _, h := range []fixture.HeldStatement{held, heldFalse} {
		d := fixture.ControlsReferenceDiagnostic("42501", "permission denied for view information_schema_catalog_name")
		s := fixture.ErrorDiagnostic(h.SQL, d)
		s.SelectRows, s.ErrorStage = true, "execute"
		iscnRoutes(t, p, c, r, h, s)
	}
	fixture.RunStep(t, p.Ctx, admin, r, fixture.Exec("GRANT SELECT ON "+iscnRelation+" TO PUBLIC", 0))
	iscnRoutes(t, p, c, r, held, one)
	iscnRoutes(t, p, c, r, heldFalse, empty)
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
}

func TestReferenceInfoInformationSchemaCatalogNamePrivileges(t *testing.T) {
	iscnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, pool *sql.DB, r *fixture.Recorder) {
		iscnPrivileges(t, p, c, pool, r)
	})
}

func TestReferenceInfoInformationSchemaCatalogNameSmoke(t *testing.T) {
	iscnScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, pool *sql.DB, r *fixture.Recorder) {
		s := iscnStep("catalog_name", "", true)
		held := iscnHold(t, p, c, s.Query)
		fixture.RunStep(t, p.Ctx, c, r, s)
		fixture.RunStep(t, p.Ctx, held, r, s)
		null := iscnStep("catalog_name", "catalog_name=$1", false, nil)
		fixture.RunStep(t, p.Ctx, iscnHold(t, p, c, null.Query), r, null)
		d := fixture.ControlsReferenceDiagnostic("42703", `column "missing" does not exist`)
		d.Position = fixture.Position(8)
		failure := fixture.ErrorDiagnostic("SELECT missing FROM "+iscnRelation, d)
		failure.SelectRows, failure.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, c, r, failure)
		iscnPrivileges(t, p, c, pool, r)
	})
}
