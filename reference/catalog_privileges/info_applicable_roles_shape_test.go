package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoApplicableRolesShape(t *testing.T) {
	applicableRolesScenario(t, []string{"ar_shape_actor", "ar_shape_target"}, func(s *applicableRolesCase) {
		t := s.t
		fixture.PgDescriptionExecutorRows(t, s.p, s.b, s.c, fixture.Select(`SELECT a.attnum,a.attname,n.nspname,t.typname,t.typtype,t.typbasetype,a.atttypmod,a.attnotnull,a.attcollation,a.attndims,t.typnotnull FROM pg_catalog.pg_attribute a JOIN pg_catalog.pg_type t ON t.oid=a.atttypid JOIN pg_catalog.pg_namespace n ON n.oid=t.typnamespace WHERE a.attrelid='information_schema.applicable_roles'::regclass AND a.attnum>0 ORDER BY a.attnum`, []string{"attnum", "attname", "nspname", "typname", "typtype", "typbasetype", "atttypmod", "attnotnull", "attcollation", "attndims", "typnotnull"}, []string{"INT2", "NAME", "NAME", "NAME", "CHAR", "OID", "INT4", "BOOL", "OID", "INT2", "BOOL"}, [][]any{
			{int64(1), "grantee", "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false},
			{int64(2), "role_name", "information_schema", "sql_identifier", "d", int64(19), int64(-1), false, int64(950), int64(0), false},
			{int64(3), "is_grantable", "information_schema", "yes_or_no", "d", int64(1043), int64(-1), false, int64(950), int64(0), false},
		}))
		s.run(fixture.Select(`SELECT typtypmod FROM pg_catalog.pg_type WHERE oid='information_schema.yes_or_no'::regtype`, []string{"typtypmod"}, []string{"INT4"}, [][]any{{int64(7)}}))
		s.run(fixture.Select(`SELECT c.reltype=t.oid AS same_type,t.typrelid=c.oid AS same_relation,a.typelem=t.oid AS array_element,t.typarray=a.oid AS array_link FROM pg_catalog.pg_class c JOIN pg_catalog.pg_type t ON t.oid=c.reltype JOIN pg_catalog.pg_type a ON a.oid=t.typarray WHERE c.oid='information_schema.applicable_roles'::regclass`, []string{"same_type", "same_relation", "array_element", "array_link"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}))
		s.exec("CREATE ROLE ar_shape_actor")
		s.exec("CREATE ROLE ar_shape_target")
		admin, err := s.b.DB.Conn(s.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		s.exec("SET SESSION AUTHORIZATION ar_shape_actor")
		s.read()
		// Reuse direct, fresh prepared and held statements with empty metadata.
		heldAll := s.held(applicableRolesQuery)
		heldAll(s.projection(applicableRolesQuery))
		for _, populated := range []bool{false, true} {
			var want [][3]string
			if populated {
				fixture.RunStep(t, s.p.Ctx, admin, s.b.Recorder, fixture.Exec("GRANT ar_shape_target TO ar_shape_actor", 0))
				want = [][3]string{{"ar_shape_actor", "ar_shape_target", "NO"}}
			}
			s.read(want...)
			heldAll(s.projection(applicableRolesQuery, want...))
			for _, query := range []string{
				"SELECT * FROM information_schema.applicable_roles",
				applicableRolesQuery,
				applicableRolesQuery + " WHERE FALSE",
				applicableRolesQuery + " WHERE grantee='ar_shape_absent'",
				applicableRolesQuery + " WHERE role_name='ar_shape_absent'",
				applicableRolesQuery + " WHERE grantee IS NULL",
				applicableRolesQuery + " WHERE role_name IS NULL",
				applicableRolesQuery + " WHERE is_grantable IS NULL",
			} {
				var triples [][3]string
				if query == applicableRolesQuery || query == "SELECT * FROM information_schema.applicable_roles" {
					triples = want
				}
				s.readQuery(query, triples...)
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
						s.consume(rows, triples...)
					}()
				}
			}
		}
		query := applicableRolesQuery + " WHERE grantee=$1"
		held := s.held(query)
		for _, arg := range []any{nil, "ar_shape_actor", "ar_shape_absent", "ar_shape_actor"} {
			var triples [][3]string
			if arg == "ar_shape_actor" {
				triples = [][3]string{{"ar_shape_actor", "ar_shape_target", "NO"}}
			}
			step := s.projection(query, triples...)
			step.Args = []any{arg}
			s.run(step)
			s.run(fixture.Prepared(step))
			held(step)
		}
	})
}

func TestReferenceInfoApplicableRolesDiagnostics(t *testing.T) {
	applicableRolesScenario(t, []string{"ar_diag_actor", "ar_diag_target", "ar_diag_island"}, func(s *applicableRolesCase) {
		for _, role := range []string{"ar_diag_actor", "ar_diag_target", "ar_diag_island"} {
			s.exec("CREATE ROLE " + role)
		}
		s.exec("GRANT ar_diag_target TO ar_diag_actor WITH INHERIT TRUE")
		s.exec("GRANT ar_diag_island TO ar_diag_actor WITH INHERIT FALSE, SET FALSE")
		secretQuery := "SELECT rolname FROM pg_catalog.pg_authid WHERE FALSE"
		privilegedHeld := s.held(secretQuery)
		s.exec("SET SESSION AUTHORIZATION ar_diag_actor")
		want := [][3]string{{"ar_diag_actor", "ar_diag_target", "NO"}, {"ar_diag_actor", "ar_diag_island", "NO"}}
		check := func() {
			s.read(want...)
			s.run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"ar_diag_actor", "ar_diag_actor"}}))
		}
		check()
		s.missing("missing")
		s.missing("admin_option")
		identity := fixture.Select("SELECT rolname,rolpassword FROM pg_catalog.pg_roles WHERE rolname IN ('ar_diag_actor','ar_diag_target','ar_diag_island')", []string{"rolname", "rolpassword"}, []string{"NAME", "TEXT"}, [][]any{{"ar_diag_actor", "********"}, {"ar_diag_target", "********"}, {"ar_diag_island", "********"}})
		identity.Bag = true
		s.run(identity)
		s.run(fixture.Prepared(identity))
		ordinaryHeld := s.held(secretQuery) // Describe is admitted; Execute is denied.
		denied := fixture.ErrorDiagnostic(secretQuery, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table pg_authid"))
		denied.SelectRows, denied.ErrorStage = true, "execute"
		s.run(denied)
		s.run(fixture.Prepared(denied))
		privilegedHeld(denied)
		ordinaryHeld(denied)
		check()
		d := fixture.ControlsReferenceDiagnostic("42501", `permission denied to set role "ar_diag_island"`)
		for _, prepared := range []bool{false, true} {
			step := fixture.ErrorDiagnostic("SET ROLE ar_diag_island", d)
			step.Prepared, step.ErrorStage = prepared, "execute"
			s.run(step)
			check()
		}
	})
}
