package reference_test

import (
	"context"
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

func TestReferenceTransactionalViewsAuthority(t *testing.T) {
	transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
		base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		base(fixture.Exec("CREATE USER txv_owner", 0))
		base(fixture.Exec("CREATE USER txv_actor", 0))
		// Schema grants survive public-object teardown. Revoke the scenario's
		// grant before schema/role fixture cleanup, including assertion failures.
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c.ExecContext(ctx, "ROLLBACK")
			c.ExecContext(ctx, "RESET SESSION AUTHORIZATION")
			if _, err := peer.ExecContext(ctx, "REVOKE CREATE ON SCHEMA public FROM txv_owner"); err != nil {
				t.Error("schema grant cleanup", err)
			}
		})
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE SCHEMA txv_dest AUTHORIZATION txv_owner", 0),
			fixture.Exec("CREATE SCHEMA txv_addon", 0), fixture.Exec("CREATE EXTENSION unaccent SCHEMA txv_addon", 0),
			fixture.Exec("GRANT USAGE ON SCHEMA public TO PUBLIC", 0),
			fixture.Exec("GRANT CREATE ON SCHEMA public TO txv_owner", 0),
			fixture.Exec("CREATE TABLE txv_source(id INT)", 0), fixture.Exec("INSERT INTO txv_source VALUES(7)", 1),
			fixture.Exec("GRANT SELECT ON txv_source TO txv_owner", 0),
			fixture.Exec("CREATE VIEW txv_v AS SELECT id FROM txv_source", 0),
			fixture.Exec("CREATE MATERIALIZED VIEW txv_m AS SELECT id FROM txv_source", 1),
			fixture.Exec("GRANT SELECT ON txv_v,txv_m TO txv_actor", 0),
		} {
			base(s)
		}
		v, m := transactionalViewsIdentity(t, p, c, "txv_v"), transactionalViewsIdentity(t, p, c, "txv_m")
		queries := []string{
			"ALTER VIEW txv_v OWNER TO txv_owner",
			"ALTER MATERIALIZED VIEW txv_m OWNER TO txv_owner",
			"ALTER MATERIALIZED VIEW txv_m DEPENDS ON EXTENSION unaccent",
			"ALTER MATERIALIZED VIEW txv_m NO DEPENDS ON EXTENSION unaccent",
			"ALTER MATERIALIZED VIEW txv_m DEPENDS ON EXTENSION unaccent",
		}
		executors := make([]fixture.Executor, len(queries))
		for i, q := range queries {
			executors[i] = c
			if held {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				executors[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
		}
		readerQuery := "SELECT id FROM txv_v"
		reader, err := c.PrepareContext(p.Ctx, readerQuery)
		if err != nil {
			t.Fatal(err)
		}
		defer reader.Close()
		readerExec := fixture.HeldStatement{Executor: c, Statement: reader, SQL: readerQuery}
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
		for i, q := range queries {
			fixture.RunStep(t, p.Ctx, executors[i], b.Recorder, fixture.Exec(q, 0))
			if i >= 2 {
				count := int64(1)
				if i == 3 {
					count = 0
				}
				alterX(run, "txv_m", count)
			}
		}
		if got := transactionalViewsIdentity(t, p, e, "txv_v"); !reflect.DeepEqual(got, v) {
			t.Fatal(v, got)
		}
		if got := transactionalViewsIdentity(t, p, e, "txv_m"); !reflect.DeepEqual(got, m) {
			t.Fatal(m, got)
		}
		run(fixture.Select("SELECT count(*) FROM pg_class WHERE oid IN ($1,$2) AND relowner=(SELECT oid FROM pg_roles WHERE rolname='txv_owner') AND relacl IS NOT NULL", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, v[0], m[0]))
		if mode != "autocommit" {
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT count(*) FROM pg_class WHERE oid IN ($1,$2) AND relowner=(SELECT oid FROM pg_roles WHERE rolname='txv_owner')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}, v[0], m[0]))
		}
		if mode != "autocommit" {
			run(fixture.Exec("SAVEPOINT txv_authority_mark", 0))
		}
		run(fixture.Exec("SET ROLE txv_actor", 0))
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		fixture.RunStep(t, p.Ctx, readerExec, b.Recorder, fixture.Select(readerQuery, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		run(fixture.Select("SELECT id FROM txv_m", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if mode != "autocommit" {
			run(fixture.Exec("ROLLBACK TO txv_authority_mark", 0))
		} else {
			run(fixture.Exec("RESET ROLE", 0))
		}
		finish(false)
		if mode != "autocommit" {
			alterX(base, "txv_m", 0)
			base(fixture.Select("SELECT count(*) FROM pg_class WHERE oid IN ($1,$2) AND relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}, v[0], m[0]))
		}
		// Exercise fresh source authorization after ownership is committed.
		// The private owner transition above has its own catalog visibility;
		// a peer ACL change is not an oracle-established denial in that scope.
		transactionalViewsExec(t, p, b, c, held, fixture.Exec("ALTER VIEW txv_v OWNER TO txv_owner", 0))
		base(fixture.Exec("SET SESSION AUTHORIZATION txv_actor", 0))
		fixture.RunStep(t, p.Ctx, readerExec, b.Recorder, fixture.Select(readerQuery, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("REVOKE SELECT ON txv_source FROM txv_owner", 0))
		denied := fixture.ImplicitError(readerQuery, fixture.Diagnostic{Code: "42501", Message: "permission denied for table txv_source"})
		denied.SelectRows = true
		denied.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, readerExec, b.Recorder, denied)
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Exec("GRANT SELECT ON txv_source TO txv_owner", 0))
		fixture.RunStep(t, p.Ctx, readerExec, b.Recorder, fixture.Select(readerQuery, []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
		base(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Reuse a held DDL handle after a real authority transition. Preparation
		// under administrator authority cannot cache permission to alter an object.
		q := "ALTER VIEW txv_v SET(security_barrier=true)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		base(fixture.Exec("SET SESSION AUTHORIZATION txv_actor", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "must be owner of view txv_v"}))
		base(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		base(fixture.Select("SELECT id FROM txv_m", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
	})
}
