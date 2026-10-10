package reference_test

import "testing"

func TestReferenceArrayAggACL(t *testing.T) {
	const role = "aa_reader"
	roleReferenceCleanup(t, []string{role})
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.ctx, "RESET SESSION AUTHORIZATION")
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec("CREATE USER "+role, 0))
			run(referenceExec("CREATE TABLE aa_acl(id INT,v INT,a INT[])", 0))
			if b.pg {
				run(referenceExec("GRANT USAGE,CREATE ON SCHEMA public TO "+role, 0))
			}
			run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
			run(referenceExec("CREATE TABLE aa_acl_target(v INT[])", 0))
			want := controlsReferenceDiagnostic("42501", "permission denied for table aa_acl")
			for _, expr := range []string{"array_agg(v ORDER BY id)", "array_agg(a ORDER BY id)", "array_agg(v ORDER BY id) FILTER(WHERE v IS NULL)", "array_agg(a ORDER BY id) FILTER(WHERE FALSE)"} {
				for _, suffix := range []string{"", " WHERE FALSE"} {
					q := "SELECT " + expr + " FROM aa_acl" + suffix
					for _, prepared := range []bool{false, true} {
						var source referenceExecutor = conn
						if prepared {
							stmt, e := conn.PrepareContext(p.ctx, q)
							if e != nil {
								t.Fatalf("semantic prepare: %v", e)
							}
							defer stmt.Close()
							source = referenceHeldStatement{referenceExecutor: conn, statement: stmt, query: q}
						}
						rows, e := source.QueryContext(p.ctx, q)
						if rows != nil {
							rows.Close()
							t.Fatal("denial exposed cursor")
						}
						referenceAssertDiagnostic(t, e, &want)
						result, e := source.ExecContext(p.ctx, q)
						if result != nil {
							t.Fatal("denial exposed result")
						}
						referenceAssertDiagnostic(t, e, &want)
					}
				}
			}
			for _, prepared := range []bool{false, true} {
				s := referenceErrorDiagnostic("INSERT INTO aa_acl_target SELECT array_agg(a ORDER BY id) FILTER(WHERE FALSE) FROM aa_acl WHERE FALSE", want)
				s.prepared = prepared
				s.errorStage = "execute"
				run(s)
			}
			arrayAggBoth(run, arrayReferenceSelect("SELECT v FROM aa_acl_target", []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, nil, b.pg))
			s := referenceImplicitError("SELECT array_agg(missing) FROM aa_acl", referenceDiagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "18"})
			s.selectRows = true
			arrayAggBoth(run, s)
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("INSERT INTO aa_acl VALUES(1,7,'{1,NULL}'),(2,NULL,'{2,3}')", 2))
			run(referenceExec("GRANT SELECT ON aa_acl TO "+role, 0))
			run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(v ORDER BY id) FROM aa_acl", "_INT4", [][]any{{"{7,NULL}"}}, b.pg))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(a ORDER BY id) FROM aa_acl", "_INT4", [][]any{{"{{1,NULL},{2,3}}"}}, b.pg))
			run(referenceExec("RESET SESSION AUTHORIZATION", 0))
			run(referenceExec("CREATE TABLE aa_column_acl(id INT,v INT,a INT[])", 0))
			run(referenceExec("INSERT INTO aa_column_acl VALUES(1,7,'{1,NULL}'),(2,NULL,'{2,3}')", 2))
			run(referenceExec("GRANT SELECT(v) ON aa_column_acl TO "+role, 0))
			run(referenceExec("SET SESSION AUTHORIZATION "+role, 0))
			arrayAggBoth(run, arrayAggSelect("SELECT array_agg(v ORDER BY v) FROM aa_column_acl", "_INT4", [][]any{{"{7,NULL}"}}, b.pg))
			columnWant := controlsReferenceDiagnostic("42501", "permission denied for table aa_column_acl")
			for _, q := range []string{
				"SELECT array_agg(v ORDER BY id) FROM aa_column_acl",
				"SELECT array_agg(a) FROM aa_column_acl WHERE FALSE",
				"SELECT array_agg(v) FILTER(WHERE id=1) FROM aa_column_acl",
				"SELECT array_agg(v) FROM aa_column_acl GROUP BY id",
				"SELECT array_agg(v) FROM aa_column_acl HAVING count(id)>0",
				"SELECT array_agg(v) FROM aa_column_acl ORDER BY count(id)",
			} {
				for _, prepared := range []bool{false, true} {
					s := referenceErrorDiagnostic(q, columnWant)
					s.selectRows = true
					s.prepared = prepared
					s.errorStage = "execute"
					run(s)
				}
			}
		})
	}
}
