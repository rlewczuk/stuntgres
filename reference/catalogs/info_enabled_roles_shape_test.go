package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoEnabledRolesShape(t *testing.T) {
	enabledRolesScenario(t, []string{"er_shape"}, func(s *enabledRolesCase) {
		t := s.t
		fixture.PgDescriptionExecutorRows(t, s.p, s.b, s.c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.enabled_roles'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, [][]any{{int64(1), "role_name", "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false}}))
		s.run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.enabled_roles'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		s.exec("CREATE ROLE er_shape")
		s.exec("SET SESSION AUTHORIZATION er_shape")
		s.read("er_shape")
		for _, query := range []string{
			"SELECT * FROM information_schema.enabled_roles",
			enabledRolesQuery + " WHERE FALSE",
			enabledRolesQuery + " WHERE role_name='er_shape_absent'",
			enabledRolesQuery + " WHERE role_name IS NULL",
		} {
			var names []string
			if query == "SELECT * FROM information_schema.enabled_roles" {
				names = []string{"er_shape"}
			}
			step := s.projection(query, names...)
			s.run(step)
			s.run(fixture.Prepared(step))
			// Retained ColumnTypes remain valid after EOF and explicit Close.
			for _, prepared := range []bool{false, true} {
				rows, closeQuery, err := fixture.TransactionQuery(s.p.Ctx, s.c, query, prepared)
				if err != nil {
					t.Fatal(err)
				}
				func() {
					defer func() {
						if err := closeQuery(); err != nil {
							t.Error(err)
						}
					}()
					columns, err := rows.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					enabledRolesMetadata(t, columns, s.b.PG)
					count := 0
					for rows.Next() {
						var value any
						if err := rows.Scan(&value); err != nil {
							t.Fatal(err)
						}
						step.CheckRaw(t, 0, count, value)
						count++
					}
					if err := rows.Err(); err != nil || count != len(names) {
						t.Fatalf("count=%d want=%d error=%v", count, len(names), err)
					}
					if err := rows.Close(); err != nil {
						t.Fatal(err)
					}
					enabledRolesMetadata(t, columns, s.b.PG)
				}()
			}
		}
		query := enabledRolesQuery + " WHERE role_name=$1"
		held := s.held(query)
		for _, arg := range []any{nil, "er_shape", "er_shape_absent", "er_shape"} {
			var names []string
			if arg == "er_shape" {
				names = []string{"er_shape"}
			}
			step := s.projection(query, names...)
			step.Args = []any{arg}
			s.run(step)
			s.run(fixture.Prepared(step))
			held(step)
		}
	})
}

func TestReferenceInfoEnabledRolesDiagnostics(t *testing.T) {
	enabledRolesScenario(t, []string{"er_diag"}, func(s *enabledRolesCase) {
		s.exec("CREATE ROLE er_diag")
		s.exec("SET SESSION AUTHORIZATION er_diag")
		s.missing("missing")
		s.missing("admin_option")
		s.read("er_diag")
	})
}
