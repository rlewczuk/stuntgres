package reference_test

import (
	"bytes"
	"database/sql"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

// The native prerequisites are fdwScenario's actual SQL-backed registrations,
// not callable mocks. PostgreSQL installs genuine C aliases for the same SQL
// identities. Dump is not required to serialize a native callback registration.
func TestReferenceRoutineAttributesForeignProviderPersistence(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		postgresPrerequisites := func(c *sql.Conn) {
			t.Helper()
			for _, q := range []string{
				"RESET ROLE",
				"SET SESSION AUTHORIZATION stuntgres",
				"CREATE FUNCTION public.fdw_sql_handler() RETURNS fdw_handler LANGUAGE c AS '$libdir/postgres_fdw','postgres_fdw_handler'",
				"CREATE FUNCTION public.fdw_scan_only_handler() RETURNS fdw_handler LANGUAGE c AS '$libdir/postgres_fdw','postgres_fdw_handler'",
				"CREATE FUNCTION public.fdw_sql_validator(text[],oid) RETURNS void LANGUAGE c AS '$libdir/postgres_fdw','postgres_fdw_validator'",
			} {
				fixture.RunStep(t, p.Ctx, c, p.PostgresRecord, fixture.Exec(q, 0))
			}
		}
		if b.PG {
			postgresPrerequisites(c)
		}
		beforeCallbacks := r.snapshot()
		r.mu.Lock()
		beforeValidations := len(r.validations)
		r.mu.Unlock()
		noCallbacks := func() {
			t.Helper()
			r.mu.Lock()
			validations := len(r.validations)
			r.mu.Unlock()
			if got := r.snapshot(); got != beforeCallbacks || validations != beforeValidations {
				t.Fatal("routine metadata/control invoked registered provider", beforeCallbacks, got, beforeValidations, validations)
			}
		}
		run(fixture.Exec("CREATE TABLE ra_foreign_provider_data(v integer)", 0))
		run(fixture.Exec("INSERT INTO ra_foreign_provider_data VALUES(42)", 1))
		targets := []struct {
			signature, name, inputs string
			result                  int64
		}{
			{"public.fdw_sql_handler()", "fdw_sql_handler", "", 3115},
			{"public.fdw_sql_validator(text[],oid)", "fdw_sql_validator", "1009 26", 2278},
		}
		identity := func(c *sql.Conn) [][]any {
			t.Helper()
			var identities [][]any
			for _, target := range targets {
				var oid, owner int64
				var acl sql.NullString
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint,proowner::bigint,proacl::text FROM pg_proc WHERE oid=$1::regprocedure", target.signature).Scan(&oid, &owner, &acl); err != nil || oid <= 0 || owner <= 0 {
					t.Fatal("registered provider identity", target.signature, oid, owner, acl, err)
				}
				var aclValue any
				if acl.Valid {
					aclValue = acl.String
				}
				identities = append(identities, []any{oid, owner, aclValue})
			}
			return identities
		}
		original := identity(c)
		for _, target := range targets {
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("ALTER FUNCTION "+target.signature+" PARALLEL RESTRICTED COST 9", 0)
				s.Prepared = prepared
				run(s)
			}
		}
		if got := identity(c); !reflect.DeepEqual(got, original) {
			t.Fatal("metadata ALTER changed provider OID/owner/ACL", original, got)
		}
		// Native registration installs the named bootstrap owner postgres.
		// Real C prerequisites belong to their explicit creator stuntgres:
		// provider replay changes metadata, not ownership of an unchanged
		// registration. Neither contract depends on the replay session actor.
		check := func(c *sql.Conn, backend fixture.Backend, phase, owner string) {
			t.Helper()
			t.Run(phase, func(t *testing.T) {
				both := func(t *testing.T, s fixture.Step) {
					t.Helper()
					for _, prepared := range []bool{false, true} {
						route := "direct"
						if prepared {
							route = "prepared"
						}
						t.Run(route, func(t *testing.T) {
							s.Prepared = prepared
							fixture.RunStep(t, p.Ctx, c, backend.Recorder, s)
						})
					}
				}
				for _, target := range targets {
					t.Run(target.name, func(t *testing.T) {
						s := fixture.Select("SELECT proparallel::text AS parallel,procost+0::real AS cost,prorows+0::real AS rows,proretset OR false AS sets,prorettype::bigint AS result,proargtypes::text AS inputs,proowner::regrole::text AS owner,proacl::text AS acl FROM pg_proc WHERE oid=$1::regprocedure",
							[]string{"parallel", "cost", "rows", "sets", "result", "inputs", "owner", "acl"}, []string{"TEXT", "FLOAT4", "FLOAT4", "BOOL", "INT8", "TEXT", "TEXT", "TEXT"}, [][]any{{"r", float64(9), float64(0), false, target.result, target.inputs, owner, nil}}, target.signature)
						s.RawTypes = []string{"TEXT", "FLOAT4", "FLOAT4", "BOOL", "INT8", "TEXT", "TEXT", "TEXT"}
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
							columns, err := rows.ColumnTypes()
							if err != nil {
								t.Fatal(err)
							}
							fixture.ScalarAssertMetadata(t, backend.PG, columns, 0, 1, 2, 3, 4, 5, 6, 7)
							for _, i := range []int{1, 2} {
								want := reflect.TypeFor[float64]()
								if backend.PG {
									want = reflect.TypeFor[float32]()
								}
								if columns[i].ScanType() != want {
									t.Fatalf("FLOAT4 ScanType=%v want %v", columns[i].ScanType(), want)
								}
							}
						}
						both(t, s)
					})
				}
				t.Run("data", func(t *testing.T) {
					both(t, fixture.Select("SELECT v FROM ra_foreign_provider_data", []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
				})
				noCallbacks()
			})
		}
		owner := "postgres"
		if b.PG {
			owner = "stuntgres"
		}
		check(c, b, "altered", owner)
		if b.PG {
			return
		} // Only the public native replacement controls have no PG counterpart.
		q := "SELECT v FROM ra_foreign_provider_data"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := func() {
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		}
		retained := fdwHeld(t, p, b, c, q)
		held()
		var emitted []byte
		for round := range 2 {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			emitted = append([]byte(nil), dump.Bytes()...)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(emitted)); err != nil {
				t.Fatal(err)
			}
			phase := []string{"round_1", "round_2"}[round]
			check(c, b, phase+"/native_restore", "postgres")
			held()
			if p.Postgres != nil {
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer pg.Close()
					defer func() {
						if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public"); err != nil {
							t.Error("foreign replay prerequisite cleanup", err)
						}
					}()
					if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE;CREATE SCHEMA public"); err != nil {
						t.Fatal(err)
					}
					postgresPrerequisites(pg)
					registered := identity(pg)
					if _, err := pg.ExecContext(p.Ctx, string(emitted)); err != nil {
						t.Fatal("unchanged provider SQL after real C registration prerequisites", err)
					}
					check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, phase+"/emitted_sql_postgres", "stuntgres")
					if got := identity(pg); !reflect.DeepEqual(got, registered) {
						t.Fatal(phase, "emitted SQL changed genuine C registration OID/owner/ACL", registered, got)
					}
				}()
			}
		}
		fixture.RunStep(t, p.Ctx, retained, retained.recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(42)}}))
		d := stuntgres.NewDriver()
		if err := r.configure(d); err != nil {
			t.Fatal(err)
		}
		connector, err := d.OpenConnector(t.Name() + "/fresh-registered-provider")
		if err != nil {
			t.Fatal(err)
		}
		raw := sql.OpenDB(connector)
		defer raw.Close()
		recorded := fdwRecordedPool(t, connector)
		defer recorded.Close()
		if _, err := recorded.ExecContext(p.Ctx, "CREATE TYPE ra_foreign_provider_pressure AS(v integer)"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, raw, bytes.NewReader(emitted)); err != nil {
			t.Fatal("fresh Driver with matching genuine registrations", err)
		}
		clone, err := recorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer clone.Close()
		check(clone, fixture.Backend{Recorder: &fixture.Recorder{RawBackend: fixture.RawNative}}, "fresh_driver", "postgres")
		if _, err := clone.ExecContext(p.Ctx, "UPDATE ra_foreign_provider_data SET v=99"); err != nil {
			t.Fatal(err)
		}
		check(c, b, "original_after_clone_update", "postgres")
		incumbent := identity(c)
		var before bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &before); err != nil {
			t.Fatal(err)
		}
		atomic := func(phase string) {
			check(c, b, phase, "postgres")
			held()
			if got := identity(c); !reflect.DeepEqual(got, incumbent) {
				t.Fatal("failed Restore changed registered identity", incumbent, got)
			}
			run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='ra_foreign_provider_prefix'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
			var after bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before.Bytes(), after.Bytes()) {
				t.Fatal("failed Restore changed provider metadata or backing")
			}
			noCallbacks()
		}
		bad := before.String() + ";CREATE TABLE ra_foreign_provider_prefix(v integer);ALTER FUNCTION public.fdw_sql_handler() COST 0;"
		fixture.AssertControlDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), fdwDiagnostic("22023", "COST must be positive"))
		atomic("failed_restore_invalid_cost")
		failure := errors.New("foreign provider metadata late read failure")
		if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(bytes.NewReader(before.Bytes()), strings.NewReader(";CREATE TABLE ra_foreign_provider_prefix(v integer);"), &fixture.ReferenceRangeErrorReader{Err: failure})); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		atomic("failed_restore_late_read")
	})
}
