package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGPolicyMetadata(t *testing.T) {
	fixture.RoleMembershipCases(t, nil, func(s *fixture.RoleMembershipScenario) {
		columns := []string{"oid", "polname", "polrelid", "polcmd", "polpermissive", "polroles", "polqual", "polwithcheck"}
		types := []string{"OID", "NAME", "OID", "CHAR", "BOOL", "_OID", "PG_NODE_TREE", "PG_NODE_TREE"}
		for _, query := range []string{"SELECT * FROM pg_policy WHERE false", "SELECT oid,polname,polrelid,polcmd,polpermissive,polroles,polqual,polwithcheck FROM pg_catalog.pg_policy WHERE false"} {
			step := fixture.Select(query, columns, types, [][]any{})
			step.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				metadata, err := rows.ColumnTypes()
				if err != nil || len(metadata) != 8 {
					t.Fatal(metadata, err)
				}
				for i, column := range metadata {
					nullable, known := column.Nullable()
					if s.Backend.PG {
						if nullable || known {
							t.Fatal("pq nullability must remain unknown", i, nullable, known)
						}
					} else if !known || nullable != (i >= 6) {
						t.Fatal("physical policy nullability", i, nullable, known)
					}
				}
			}
			s.Run(step)
		}
		s.Run(fixture.Select("SELECT 'pg_catalog.pg_policy'::regclass::oid::text AS oid", []string{"oid"}, []string{"TEXT"}, [][]any{{"3256"}}))
		s.Run(fixture.Select("SELECT attname,atttypid::text,attnum,attnotnull,attndims,attcollation::text FROM pg_attribute WHERE attrelid='pg_catalog.pg_policy'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "attnum", "attnotnull", "attndims", "attcollation"}, []string{"NAME", "TEXT", "INT2", "BOOL", "INT2", "TEXT"}, [][]any{
			{"oid", "26", int64(1), true, int64(0), "0"},
			{"polname", "19", int64(2), true, int64(0), "950"},
			{"polrelid", "26", int64(3), true, int64(0), "0"},
			{"polcmd", "18", int64(4), true, int64(0), "0"},
			{"polpermissive", "16", int64(5), true, int64(0), "0"},
			{"polroles", "1028", int64(6), true, int64(1), "0"},
			{"polqual", "194", int64(7), false, int64(0), "950"},
			{"polwithcheck", "194", int64(8), false, int64(0), "950"},
		}))
		s.Run(fixture.Select("SELECT c.relkind,c.relhasindex,t.typrelid=c.oid AS row_link,a.typelem=t.oid AS array_link FROM pg_class c JOIN pg_type t ON t.oid=c.reltype JOIN pg_type a ON a.oid=t.typarray WHERE c.oid=3256", []string{"relkind", "relhasindex", "row_link", "array_link"}, []string{"CHAR", "BOOL", "BOOL", "BOOL"}, [][]any{{"r", true, true, true}}))
		policySetup(s)
		s.Exec("CREATE POLICY p ON t USING(id>0)")
		s.Exec("CREATE POLICY p ON u WITH CHECK(allowed)")
		oid := policyOID(s, "p")
		s.Run(fixture.Select("SELECT oid<>$1::oid AS distinct_identity,polqual IS NULL AS no_using,polwithcheck IS NOT NULL AS has_check FROM pg_policy WHERE polrelid='u'::regclass AND polname='p'", []string{"distinct_identity", "no_using", "has_check"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}, oid))
		pgPolicyFacts(s, "t", "p", "*", true, "public", "(id > 0)", "")
		s.Run(fixture.Select("SELECT polqual IS NOT NULL AS has_tree,polqual::text<>'' AS structural_output,polwithcheck IS NULL AS no_check FROM pg_policy WHERE polrelid='t'::regclass AND polname='p'", []string{"has_tree", "structural_output", "no_check"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		pgPolicyPopulatedStar(s, oid)
	})
}

func TestReferencePGPolicyDefinitions(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"pgpolicy_a", "pgpolicy_b"}, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		for _, tc := range []struct{ command, code, clause, using, check string }{
			{"ALL", "*", " USING(id>0)", "(id > 0)", ""},
			{"SELECT", "r", " USING(allowed)", "allowed", ""},
			{"INSERT", "a", " WITH CHECK(allowed)", "", "allowed"},
			{"UPDATE", "w", " USING(id>0) WITH CHECK(allowed)", "(id > 0)", "allowed"},
			{"DELETE", "d", " USING(allowed)", "allowed", ""},
		} {
			for _, mode := range []string{"PERMISSIVE", "RESTRICTIVE"} {
				s.Exec("CREATE POLICY p ON t AS " + mode + " FOR " + tc.command + " TO pgpolicy_b,pgpolicy_a,pgpolicy_b" + tc.clause)
				oid := policyOID(s, "p")
				pgPolicyFacts(s, "t", "p", tc.code, mode == "PERMISSIVE", "pgpolicy_b,pgpolicy_a,pgpolicy_b", tc.using, tc.check)
				s.Exec("ALTER POLICY p ON t")
				policySameOID(s, "p", oid)
				s.Exec("ALTER POLICY p ON t RENAME TO renamed")
				policySameOID(s, "renamed", oid)
				pgPolicyFacts(s, "t", "renamed", tc.code, mode == "PERMISSIVE", "pgpolicy_b,pgpolicy_a,pgpolicy_b", tc.using, tc.check)
				s.Exec("DROP POLICY renamed ON t")
				s.Run(pgPolicyAbsent("t", "renamed"))
				s.Exec("CREATE POLICY p ON t")
				if got := policyOID(s, "p"); got == oid {
					s.T.Fatal("recreated policy reused retired identity", oid)
				}
				pgPolicyFacts(s, "t", "p", "*", true, "public", "", "")
				s.Exec("DROP POLICY p ON t")
			}
		}
		s.Exec("CREATE POLICY p ON t TO pgpolicy_b,pgpolicy_a,pgpolicy_b")
		s.Exec("ALTER ROLE pgpolicy_b RENAME TO pgpolicy_renamed")
		defer s.C.ExecContext(s.P.Ctx, "ALTER ROLE pgpolicy_renamed RENAME TO pgpolicy_b")
		pgPolicyFacts(s, "t", "p", "*", true, "pgpolicy_renamed,pgpolicy_a,pgpolicy_renamed", "", "")
	})
}

func pgPolicyPopulatedStar(s *fixture.RoleMembershipScenario, oid int64) {
	s.T.Helper()
	var relation int64
	if err := s.C.QueryRowContext(s.P.Ctx, "SELECT 't'::regclass::oid").Scan(&relation); err != nil {
		s.T.Fatal(err)
	}
	const query = "SELECT * FROM pg_policy WHERE polrelid='t'::regclass AND polname='p'"
	var rows *sql.Rows
	var err error
	if s.Prepared {
		statement, e := s.C.PrepareContext(s.P.Ctx, query)
		if e != nil {
			s.T.Fatal(e)
		}
		defer statement.Close()
		rows, err = statement.QueryContext(s.P.Ctx)
	} else {
		rows, err = s.C.QueryContext(s.P.Ctx, query)
	}
	if err != nil {
		s.T.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		s.T.Fatal("missing populated policy row", rows.Err())
	}
	var gotOID, gotRelation int64
	var name, command, roles string
	var permissive bool
	var using, check sql.NullString
	if err := rows.Scan(&gotOID, &name, &gotRelation, &command, &permissive, &roles, &using, &check); err != nil {
		s.T.Fatal(err)
	}
	if gotOID != oid || name != "p" || gotRelation != relation || command != "*" || !permissive || roles != "{0}" || !using.Valid || using.String == "" || check.Valid {
		s.T.Fatal("populated eight-column policy row", gotOID, name, gotRelation, command, permissive, roles, using, check)
	}
	if rows.Next() || rows.Err() != nil {
		s.T.Fatal("populated policy tail", rows.Err())
	}
}
