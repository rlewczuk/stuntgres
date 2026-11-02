package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTablespaceLifecycleSyntax(t *testing.T) {
	tablespaceLifecycleScenario(t, nil, func(t *testing.T, s *lifecycleCase) {
		for _, tc := range []struct{ q, token string }{
			{"ALTER TABLESPACE pg_default RENAME", ""},
			{"ALTER TABLESPACE pg_default RENAME TO", ""},
			{"ALTER TABLESPACE IF EXISTS pg_default RENAME TO ts_lifecycle_syntax", "EXISTS"},
			{"ALTER TABLESPACE public.pg_default RENAME TO ts_lifecycle_syntax", "."},
			{"ALTER TABLESPACE pg_default LOCATION '/tmp/x'", "LOCATION"},
			{"DROP TABLESPACE", ""},
			{"DROP TABLESPACE public.pg_default", "."},
			{"DROP TABLESPACE pg_default,pg_global", ","},
			{"DROP TABLESPACE pg_default CASCADE", "CASCADE"},
			{"DROP TABLESPACE pg_default RESTRICT", "RESTRICT"},
			{"ALTER TABLESPACE pg_default OWNER TO", ""},
			{"PREPARE ts_lifecycle_sql AS DROP TABLESPACE pg_default", "DROP"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := tablespaceDiagnostic("42601", "syntax error at end of input")
				d.Position = fixture.Position(len(tc.q) + 1)
				if tc.token != "" {
					d.Message = fmt.Sprintf("syntax error at or near %q", tc.token)
					d.Position = fixture.Position(strings.Index(tc.q, tc.token) + 1)
				}
				s.error(t, tc.q, d, true)
			})
		}
		for _, recipient := range []string{"none", "\"none\""} {
			q := "ALTER TABLESPACE pg_default OWNER TO " + recipient
			d := tablespaceDiagnostic("42939", "role name \"none\" is reserved")
			d.Position = fixture.Position(strings.Index(q, recipient) + 1)
			s.error(t, q, d, true)
		}
		q := "ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_never; DROP TABLESPACE"
		d := tablespaceDiagnostic("42601", "syntax error at end of input")
		d.Position = fixture.Position(len(q) + 1)
		s.error(t, q, d, true)
		s.names(t, "pg_default", "pg_global")
		// Identifier clipping occurs during parsing, including actual Prepare.
		for _, prepared := range []bool{false, true} {
			s.begin(t)
			long := "ts_lifecycle_" + strings.Repeat("a", 60)
			clipped := long[:63]
			*s.notices = nil
			step := fixture.Exec("ALTER TABLESPACE pg_default RENAME TO "+long, 0)
			step.Prepared = prepared
			s.step(t, step)
			s.noticesWant(t, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf("identifier %q will be truncated to %q", long, clipped)})
			s.names(t, clipped, "pg_global")
			s.rollback(t)
		}
	})
}

func TestReferenceTablespaceLifecycleRename(t *testing.T) {
	tablespaceLifecycleScenario(t, nil, func(t *testing.T, s *lifecycleCase) {
		for _, prepared := range []bool{false, true} {
			t.Run(lifecycleRoute(prepared), func(t *testing.T) {
				s.begin(t)
				defer s.rollback(t)
				s.exec(t, "CREATE ROLE ts_lifecycle_owner")
				s.exec(t, "CREATE ROLE ts_lifecycle_reader")
				s.exec(t, "CREATE ROLE ts_lifecycle_member")
				s.exec(t, "GRANT ts_lifecycle_owner TO ts_lifecycle_member WITH INHERIT TRUE, SET FALSE")
				s.error(t, "ALTER TABLESPACE ts_lifecycle_absent RENAME TO pg_reserved", lifecycleMissing("ts_lifecycle_absent"), false)
				s.error(t, "ALTER TABLESPACE pg_default RENAME TO pg_default", lifecycleReserved("pg_default"), false)
				s.exec(t, "GRANT CREATE ON TABLESPACE pg_default TO ts_lifecycle_reader")
				s.exec(t, "SET ROLE ts_lifecycle_reader")
				for _, dest := range []string{"pg_reserved", "pg_global", "ts_lifecycle_new"} {
					s.error(t, "ALTER TABLESPACE pg_default RENAME TO "+dest, tablespaceDiagnostic("42501", "permission denied for tablespace pg_default"), false)
				}
				s.exec(t, "RESET ROLE")
				// Prepare before changing effective authority, then execute again
				// after rename to prove both privilege and name re-resolution.
				q := "ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_held"
				held, err := s.c.PrepareContext(s.p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer held.Close()
				s.exec(t, "SET ROLE ts_lifecycle_reader")
				s.exec(t, "SAVEPOINT ts_lifecycle_held_error")
				_, err = held.ExecContext(s.p.Ctx)
				d := tablespaceDiagnostic("42501", "permission denied for tablespace pg_default")
				fixture.AssertDiagnostic(t, err, &d)
				s.exec(t, "ROLLBACK TO ts_lifecycle_held_error")
				s.exec(t, "RELEASE ts_lifecycle_held_error")
				s.exec(t, "RESET ROLE")
				s.exec(t, "ALTER TABLESPACE pg_default OWNER TO ts_lifecycle_owner")
				s.exec(t, "SET ROLE ts_lifecycle_member")
				step := fixture.Exec("ALTER TABLESPACE pg_default RENAME TO ts_lifecycle_renamed", 0)
				step.Prepared = prepared
				s.step(t, step)
				s.owner(t, "ts_lifecycle_renamed", "ts_lifecycle_owner", "{ts_lifecycle_owner=C/ts_lifecycle_owner,ts_lifecycle_reader=C/ts_lifecycle_owner}")
				s.count(t, "SELECT count(*) AS n FROM pg_tablespace WHERE spcname='pg_default'", 0)
				s.error(t, "ALTER TABLESPACE ts_lifecycle_renamed RENAME TO ts_lifecycle_renamed", tablespaceDiagnostic("42710", "tablespace \"ts_lifecycle_renamed\" already exists"), false)
				s.error(t, "ALTER TABLESPACE ts_lifecycle_renamed RENAME TO pg_global", lifecycleReserved("pg_global"), false)
				s.exec(t, "RESET ROLE")
				s.exec(t, "SAVEPOINT ts_lifecycle_stale")
				_, err = held.ExecContext(s.p.Ctx)
				d = lifecycleMissing("pg_default")
				fixture.AssertDiagnostic(t, err, &d)
				s.exec(t, "ROLLBACK TO ts_lifecycle_stale")
				s.exec(t, "RELEASE ts_lifecycle_stale")
				// Transaction admission precedes duplicate/path validation. The
				// semantic precedence witnesses use committed native/PG phases.
				s.error(t, tablespaceSQL("ts_lifecycle_renamed", "", tablespaceAbsent), tablespaceDiagnostic("25001", "CREATE TABLESPACE cannot run inside a transaction block"), false)
				s.error(t, tablespaceSQL("ts_lifecycle_renamed", "", "relative"), tablespaceDiagnostic("25001", "CREATE TABLESPACE cannot run inside a transaction block"), false)
				// Uppercase PG_ is not the lowercase reserved prefix; quoting is
				// preserved, including an embedded quote and a space.
				s.utility(t, "ALTER TABLESPACE ts_lifecycle_renamed RENAME TO \"PG_lifecycle\"", prepared)
				s.utility(t, "ALTER TABLESPACE pg_global RENAME TO ts_lifecycle_duplicate", prepared)
				s.error(t, "ALTER TABLESPACE \"PG_lifecycle\" RENAME TO ts_lifecycle_duplicate", tablespaceDiagnostic("42710", "tablespace \"ts_lifecycle_duplicate\" already exists"), false)
				s.utility(t, "ALTER TABLESPACE ts_lifecycle_duplicate RENAME TO \"ts lifecycle\"\"global\"", prepared)
				s.both(t, fixture.Select("SELECT oid::bigint AS oid,spcname FROM pg_tablespace ORDER BY oid", []string{"oid", "spcname"}, []string{"INT8", "NAME"}, [][]any{{int64(1663), "PG_lifecycle"}, {int64(1664), "ts lifecycle\"global"}}))
			})
			s.names(t, "pg_default", "pg_global")
		}
	})
}
