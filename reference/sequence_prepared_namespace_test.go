//go:build reference

package reference_test

import (
	"database/sql"
	"testing"
)

func referenceSequencePreparedNamespace(t *testing.T) {
	for _, scenario := range []string{"NamespaceCreation", "UserRolePath", "NumericMissingInput", "ParameterMissing", "SchemaUsageInvalidation", "OwnerTransition", "TemporaryPersistenceRejected", "RevokedMemory"} {
		t.Run(scenario, func(t *testing.T) {
			p := newReferencePair(t)
			backends := []struct {
				name string
				db   *sql.DB
			}{{"native", p.memory}, {"postgres", p.postgres}}
			if scenario == "SchemaUsageInvalidation" || scenario == "OwnerTransition" || scenario == "TemporaryPersistenceRejected" || scenario == "RevokedMemory" {
				backends = backends[1:]
			} // Native real authority witnesses use internal ACL and catalog identity APIs.
			for _, backend := range backends {
				t.Run(backend.name, func(t *testing.T) {
					c, err := backend.db.Conn(p.ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					exec := func(q string) { t.Helper(); sequenceProbeExec(t, p.ctx, c, q) }
					hold := func(q string) *sql.Stmt {
						t.Helper()
						s, err := c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						t.Cleanup(func() { s.Close() })
						return s
					}
					value := func(s *sql.Stmt, want int64) {
						t.Helper()
						var got int64
						if err := s.QueryRowContext(p.ctx).Scan(&got); err != nil || got != want {
							t.Fatal(got, want, err)
						}
					}
					if backend.name == "postgres" {
						defer func() {
							exec("RESET SESSION AUTHORIZATION")
							exec("SET search_path TO public")
							for _, name := range []string{"a", "b", "seq_path_reader", "seq_path_reader2"} {
								exec("DROP SCHEMA IF EXISTS " + name + " CASCADE")
							}
							exec("DROP ROLE IF EXISTS seq_path_reader,seq_path_reader2")
						}()
					}
					switch scenario {
					case "NamespaceCreation":
						for _, q := range []string{"CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
							exec(q)
						}
						s := hold("SELECT nextval('s')")
						value(s, 10)
						exec("CREATE SCHEMA a")
						exec("CREATE SEQUENCE a.s START 100")
						value(s, 100)
						exec("DROP SCHEMA a CASCADE")
						value(s, 11)
					case "UserRolePath":
						for _, q := range []string{"CREATE USER seq_path_reader", "CREATE USER seq_path_reader2", "CREATE SCHEMA seq_path_reader AUTHORIZATION seq_path_reader", "CREATE SCHEMA seq_path_reader2 AUTHORIZATION seq_path_reader2", "SET SESSION AUTHORIZATION seq_path_reader", "CREATE SEQUENCE seq_path_reader.s START 30", "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION seq_path_reader2", "CREATE SEQUENCE seq_path_reader2.s START 300", "RESET SESSION AUTHORIZATION", "SET SESSION AUTHORIZATION seq_path_reader", `SET search_path TO "$user",public`} {
							exec(q)
						}
						s := hold("SELECT nextval('s')")
						value(s, 30)
						exec("RESET SESSION AUTHORIZATION")
						exec("SET SESSION AUTHORIZATION seq_path_reader2")
						value(s, 300)
					case "NumericMissingInput":
						for _, input := range []string{"0", "-", "4294967295"} {
							s := hold("SELECT nextval('" + input + "'::regclass)")
							var value int64
							err := s.QueryRowContext(p.ctx).Scan(&value)
							oid := input
							if oid == "-" {
								oid = "0"
							}
							want := controlsReferenceDiagnostic("XX000", "could not open relation with OID "+oid)
							referenceAssertDiagnostic(t, err, &want)
						}
					case "ParameterMissing":
						exec("CREATE SEQUENCE s START 14")
						s := hold("SELECT nextval($1)")
						var value int64
						err := s.QueryRowContext(p.ctx, "missing").Scan(&value)
						want := controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
						want.Context = `unnamed portal parameter $1 = '...'`
						referenceAssertDiagnostic(t, err, &want)
						if err := s.QueryRowContext(p.ctx, "s").Scan(&value); err != nil || value != 14 {
							t.Fatal(value, err)
						}
					case "SchemaUsageInvalidation":
						for _, q := range []string{"CREATE USER seq_path_reader", "CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE a.s START 100", "CREATE SEQUENCE b.s START 10", "GRANT USAGE ON SCHEMA b TO seq_path_reader", "GRANT USAGE ON SEQUENCE a.s,b.s TO seq_path_reader", "SET SESSION AUTHORIZATION seq_path_reader", "SET search_path TO a,b"} {
							exec(q)
						}
						s, qualified := hold("SELECT nextval('s')"), hold("SELECT nextval('b.s')")
						exec("RESET SESSION AUTHORIZATION")
						exec("GRANT USAGE ON SCHEMA a TO seq_path_reader")
						exec("SET SESSION AUTHORIZATION seq_path_reader")
						value(s, 100)
						exec("RESET SESSION AUTHORIZATION")
						exec("REVOKE USAGE ON SCHEMA b FROM seq_path_reader")
						exec("SET SESSION AUTHORIZATION seq_path_reader")
						var v int64
						err := qualified.QueryRowContext(p.ctx).Scan(&v)
						want := controlsReferenceDiagnostic("42501", "permission denied for schema b")
						want.Position = "16"
						referenceAssertDiagnostic(t, err, &want)
					case "OwnerTransition":
						for _, q := range []string{"CREATE USER seq_path_reader", "CREATE SCHEMA a", "CREATE SCHEMA b", "GRANT CREATE ON SCHEMA b TO seq_path_reader", "CREATE SEQUENCE b.s START 10", "SET search_path TO a,b"} {
							exec(q)
						}
						s := hold("SELECT nextval('s')")
						exec("CREATE SEQUENCE a.s START 100")
						exec("ALTER SEQUENCE b.s OWNER TO seq_path_reader")
						value(s, 100)
					case "RevokedMemory":
						defer func() {
							exec("RESET SESSION AUTHORIZATION")
							exec("DROP SEQUENCE IF EXISTS public.no_shadow")
							exec("REVOKE USAGE ON SCHEMA public FROM seq_path_reader")
						}()
						for _, q := range []string{"CREATE USER seq_path_reader", "CREATE SEQUENCE no_shadow CACHE 10", "GRANT USAGE ON SCHEMA public TO seq_path_reader", "GRANT ALL ON SEQUENCE no_shadow TO seq_path_reader", "SET SESSION AUTHORIZATION seq_path_reader"} {
							exec(q)
						}
						sequenceProbeValue(t, p.ctx, c, "SELECT nextval('no_shadow')", 1)
						statements := []*sql.Stmt{hold("SELECT nextval('no_shadow')"), hold("SELECT currval('no_shadow')"), hold("SELECT lastval()"), hold("SELECT setval('no_shadow',40)")}
						exec("RESET SESSION AUTHORIZATION")
						exec("REVOKE ALL ON SEQUENCE no_shadow FROM seq_path_reader")
						exec("SET SESSION AUTHORIZATION seq_path_reader")
						for _, s := range statements {
							var v int64
							err := s.QueryRowContext(p.ctx).Scan(&v)
							want := controlsReferenceDiagnostic("42501", "permission denied for sequence no_shadow")
							referenceAssertDiagnostic(t, err, &want)
						}
						exec("RESET SESSION AUTHORIZATION")
						exec("GRANT USAGE ON SEQUENCE no_shadow TO seq_path_reader")
						exec("SET SESSION AUTHORIZATION seq_path_reader")
						value(statements[0], 2)
						sequenceProbeValue(t, p.ctx, c, "SELECT currval('no_shadow')", 2)
						sequenceProbeValue(t, p.ctx, c, "SELECT lastval()", 2)
					case "TemporaryPersistenceRejected":
						exec("CREATE TEMP SEQUENCE s")
						var schema string
						if err := c.QueryRowContext(p.ctx, "SELECT relnamespace::regnamespace::text FROM pg_class WHERE oid='s'::regclass").Scan(&schema); err != nil {
							t.Fatal(err)
						}
						for _, action := range []string{"LOGGED", "UNLOGGED"} {
							_, err := c.ExecContext(p.ctx, "ALTER SEQUENCE s SET "+action)
							want := controlsReferenceDiagnostic("42P16", `cannot change logged status of table "s" because it is temporary`)
							want.Schema, want.Table = schema, "s"
							referenceAssertDiagnostic(t, err, &want)
						}
					}
				})
			}
		})
	}
}
