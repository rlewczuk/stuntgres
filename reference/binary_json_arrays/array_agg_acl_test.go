package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArrayAggACL(t *testing.T) {
	const role = "aa_reader"
	fixture.RoleReferenceCleanup(t, []string{role})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec("CREATE USER "+role, 0))
			run(fixture.Exec("CREATE TABLE aa_acl(id INT,v INT,a INT[])", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			fixture.WithPublicSchemaOwner(t, p, b, role, func() { run(fixture.Exec("CREATE TABLE aa_acl_target(v INT[])", 0)) })
			want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table aa_acl")
			for _, expr := range []string{"array_agg(v ORDER BY id)", "array_agg(a ORDER BY id)", "array_agg(v ORDER BY id) FILTER(WHERE v IS NULL)", "array_agg(a ORDER BY id) FILTER(WHERE FALSE)"} {
				for _, suffix := range []string{"", " WHERE FALSE"} {
					q := "SELECT " + expr + " FROM aa_acl" + suffix
					for _, prepared := range []bool{false, true} {
						var source fixture.Executor = conn
						if prepared {
							stmt, e := conn.PrepareContext(p.Ctx, q)
							if e != nil {
								t.Fatalf("semantic prepare: %v", e)
							}
							defer stmt.Close()
							source = fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
						}
						rows, e := source.QueryContext(p.Ctx, q)
						if rows != nil {
							rows.Close()
							t.Fatal("denial exposed cursor")
						}
						fixture.AssertDiagnostic(t, e, &want)
						result, e := source.ExecContext(p.Ctx, q)
						if result != nil {
							t.Fatal("denial exposed result")
						}
						fixture.AssertDiagnostic(t, e, &want)
					}
				}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("INSERT INTO aa_acl_target SELECT array_agg(a ORDER BY id) FILTER(WHERE FALSE) FROM aa_acl WHERE FALSE", want)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				run(s)
			}
			arrayAggBoth(run, fixture.ArrayReferenceSelect("SELECT v FROM aa_acl_target", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, nil, b.PG))
			s := fixture.ImplicitError("SELECT array_agg(missing) FROM aa_acl", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "18"})
			s.SelectRows = true
			arrayAggBoth(run, s)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("INSERT INTO aa_acl VALUES(1,7,'{1,NULL}'),(2,NULL,'{2,3}')", 2))
			run(fixture.Exec("GRANT SELECT ON aa_acl TO "+role, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(v ORDER BY id) FROM aa_acl", "_INT4", [][]any{{"{7,NULL}"}}, b.PG))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(a ORDER BY id) FROM aa_acl", "_INT4", [][]any{{"{{1,NULL},{2,3}}"}}, b.PG))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("CREATE TABLE aa_column_acl(id INT,v INT,a INT[])", 0))
			run(fixture.Exec("INSERT INTO aa_column_acl VALUES(1,7,'{1,NULL}'),(2,NULL,'{2,3}')", 2))
			run(fixture.Exec("GRANT SELECT(v) ON aa_column_acl TO "+role, 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(v ORDER BY v) FROM aa_column_acl", "_INT4", [][]any{{"{7,NULL}"}}, b.PG))
			columnWant := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table aa_column_acl")
			for _, q := range []string{
				"SELECT array_agg(v ORDER BY id) FROM aa_column_acl",
				"SELECT array_agg(a) FROM aa_column_acl WHERE FALSE",
				"SELECT array_agg(v) FILTER(WHERE id=1) FROM aa_column_acl",
				"SELECT array_agg(v) FROM aa_column_acl GROUP BY id",
				"SELECT array_agg(v) FROM aa_column_acl HAVING count(id)>0",
				"SELECT array_agg(v) FROM aa_column_acl ORDER BY count(id)",
			} {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, columnWant)
					s.SelectRows = true
					s.Prepared = prepared
					s.ErrorStage = "execute"
					run(s)
				}
			}
		})
	}
}
