package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceAlterTablespaceSyntax(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsSeed(t, p, b, c)
		for _, q := range []string{"ALTER TABLE ONLY t SET TABLESPACE pg_default", "ALTER TABLE ONLY (t) SET TABLESPACE pg_default", "ALTER TABLE t * SET TABLESPACE \"pg_default\"", "ALTER TABLE IF EXISTS t SET TABLESPACE pg_default", "ALTER TABLE ALL IN TABLESPACE pg_default OWNED BY CURRENT_USER,CURRENT_ROLE,SESSION_USER SET TABLESPACE pg_default NOWAIT"} {
			atsRun(t, p, b, c, fixture.Prepared(fixture.Exec(q, 0)))
		}
		for _, tc := range []struct{ q, token string }{
			{"ALTER TABLE t SET TABLESPACE public.pg_default", "."},
			{"ALTER TABLE t SET TABLESPACE 'pg_default'", "'pg_default'"},
			{"ALTER TABLE t SET TABLESPACE $1", "$1"},
			{"ALTER TABLE t SET TABLESPACE pg_default NOWAIT", "NOWAIT"},
			{"ALTER TABLE IF EXISTS ALL IN TABLESPACE pg_default SET TABLESPACE pg_default", "ALL"},
			{"ALTER TABLE ALL IN TABLESPACE pg_default SET TABLESPACE pg_default, ADD COLUMN x INT", ","},
			{"ALTER TABLE t SET TABLESPACE pg_default trailing", "trailing"},
		} {
			for _, prepared := range []bool{false, true} {
				want := atsDiagnostic("42601", "syntax error at or near \""+tc.token+"\"")
				want.Position = strconv.Itoa(strings.Index(tc.q, tc.token) + 1)
				s := fixture.ErrorDiagnostic(tc.q, want)
				s.Prepared = prepared
				s.ErrorStage = "execute"
				if prepared {
					s.ErrorStage = "prepare"
				}
				atsRun(t, p, b, c, s)
			}
		}
		q := "ALTER TABLE ALL IN TABLESPACE pg_default OWNED BY none SET TABLESPACE pg_default"
		want := atsDiagnostic("42939", `role name "none" is reserved`)
		want.Position = "51"
		s := fixture.ErrorDiagnostic(q, want)
		s.ErrorStage = "execute"
		atsRun(t, p, b, c, s)
		s.Prepared = true
		s.ErrorStage = "prepare"
		atsRun(t, p, b, c, s)
		q = "ALTER TABLE t ADD COLUMN should_not_exist INT; ALTER TABLE t SET TABLESPACE pg_default trailing"
		want = atsDiagnostic("42601", `syntax error at or near "trailing"`)
		want.Position = strconv.Itoa(strings.Index(q, "trailing") + 1)
		atsRun(t, p, b, c, fixture.ErrorDiagnostic(q, want))
		atsRun(t, p, b, c, fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='t'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"id"}, {"note"}}))
		var notices []fixture.Diagnostic
		_, noticeConn, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		fixture.RunStep(t, p.Ctx, noticeConn, rec, fixture.Exec("ALTER TABLE IF EXISTS ats_absent SET TABLESPACE ats_missing", 0))
		wantNotice := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `relation "ats_absent" does not exist, skipping`}
		if len(notices) != 1 || notices[0] != wantNotice {
			t.Fatalf("notices: %+v", notices)
		}
	})
}

func TestReferenceAlterTablespaceDiagnostics(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsSeed(t, p, b, c)
		atsRoles(t, p, b, c)
		for _, tc := range []struct{ q, code, message string }{
			{"ALTER TABLE ats_absent SET TABLESPACE ats_missing", "42P01", `relation "ats_absent" does not exist`},
			{"ALTER TABLE ats_missing.t SET TABLESPACE ats_missing", "3F000", `schema "ats_missing" does not exist`},
			{"ALTER TABLE t SET TABLESPACE ats_missing", "42704", `tablespace "ats_missing" does not exist`},
			{"ALTER TABLE t SET TABLESPACE pg_global", "22023", "only shared relations can be placed in pg_global tablespace"},
			{"ALTER TABLE t SET TABLESPACE pg_default, SET TABLESPACE pg_default", "42601", "cannot have multiple SET TABLESPACE subcommands"},
			{"ALTER TABLE t SET TABLESPACE pg_default, SET TABLESPACE ats_missing", "42704", `tablespace "ats_missing" does not exist`},
			{"ALTER TABLE pg_catalog.pg_class SET TABLESPACE pg_default", "42501", `permission denied: "pg_class" is a system catalog`},
		} {
			atsError(t, p, b, c, tc.q, atsDiagnostic(tc.code, tc.message))
		}
		atsExec(t, p, b, c, "GRANT SELECT,UPDATE ON t TO ats_reader")
		atsExec(t, p, b, c, "SET ROLE ats_reader")
		atsError(t, p, b, c, "ALTER TABLE t SET TABLESPACE ats_missing", atsDiagnostic("42501", "must be owner of table t"))
		atsExec(t, p, b, c, "RESET ROLE")
		atsExec(t, p, b, c, "SET ROLE ats_owner")
		atsExec(t, p, b, c, "CREATE TABLE ats_owned(a INT)")
		atsExec(t, p, b, c, "RESET ROLE")
		atsExec(t, p, b, c, "REVOKE CREATE ON TABLESPACE pg_default FROM PUBLIC")
		atsExec(t, p, b, c, "SET ROLE ats_member")
		atsExec(t, p, b, c, "ALTER TABLE ats_owned SET TABLESPACE pg_default")
		atsError(t, p, b, c, "ALTER TABLE ats_owned SET TABLESPACE pg_global", atsDiagnostic("42501", "permission denied for tablespace pg_global"))
		atsExec(t, p, b, c, "RESET ROLE")
		atsExec(t, p, b, c, "CREATE SCHEMA ats_private")
		atsExec(t, p, b, c, "CREATE TABLE ats_private.hidden(a INT)")
		atsExec(t, p, b, c, "SET ROLE ats_reader")
		atsError(t, p, b, c, "ALTER TABLE ats_private.hidden SET TABLESPACE pg_default", atsDiagnostic("42501", "permission denied for schema ats_private"))
		atsExec(t, p, b, c, "RESET ROLE")
		atsExec(t, p, b, c, "BEGIN READ ONLY")
		atsRun(t, p, b, c, fixture.ErrorDiagnostic("ALTER TABLE t SET TABLESPACE ats_missing", atsDiagnostic("25006", "cannot execute ALTER TABLE in a read-only transaction")))
		atsRun(t, p, b, c, fixture.ErrorDiagnostic("ALTER TABLE t SET TABLESPACE pg_default", atsDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
		atsExec(t, p, b, c, "ROLLBACK")
		atsRun(t, p, b, c, atsRows())
	})
}

func TestReferenceAlterTablespacePreparationPrecedence(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsSeed(t, p, b, c)
		atsRun(t, p, b, c, fixture.Exec("CREATE MATERIALIZED VIEW ats_precedence_mv AS SELECT id FROM t", 1))
		wrongKind := atsDiagnostic("42809", `ALTER action ADD COLUMN cannot be performed on relation "ats_precedence_mv"`)
		wrongKind.Detail = "This operation is not supported for materialized views."
		badCheck := atsDiagnostic("23514", `check constraint "ats_bad" of relation "t" is violated by some row`)
		badCheck.Schema, badCheck.Table, badCheck.Constraint = "public", "t", "ats_bad"
		incompatibleType := atsDiagnostic("42804", `column "note" cannot be cast automatically to type integer`)
		incompatibleType.Hint = `You might need to specify "USING note::integer".`
		missingType := atsDiagnostic("42704", `type "ats_missing_type" does not exist`)
		missingType.Position = strconv.Itoa(strings.Index("ALTER TABLE t ALTER COLUMN id TYPE ats_missing_type", "ats_missing_type") + 1)
		invalidUsing := atsDiagnostic("42703", `column "absent" does not exist`)
		invalidUsing.Position = strconv.Itoa(strings.Index("ALTER TABLE t ALTER COLUMN id TYPE BIGINT USING absent", "absent") + 1)
		incompatibleUsing := atsDiagnostic("42804", `result of USING clause for column "note" cannot be cast automatically to type integer`)
		incompatibleUsing.Hint = "You might need to add an explicit cast."
		missingCollation := atsDiagnostic("42704", `collation "ats_missing_collation" for encoding "UTF8" does not exist`)
		missingCollation.Position = strconv.Itoa(strings.Index("ALTER TABLE t ALTER COLUMN note TYPE TEXT COLLATE ats_missing_collation", "COLLATE") + 1)
		invalidCollation := atsDiagnostic("42804", "collations are not supported by type integer")
		invalidCollation.Position = strconv.Itoa(strings.Index(`ALTER TABLE t ALTER COLUMN id TYPE INT COLLATE "C"`, "COLLATE") + 1)
		for _, tc := range []struct {
			q    string
			want fixture.Diagnostic
		}{
			{"ALTER TABLE t ALTER COLUMN absent TYPE INT, SET TABLESPACE ats_missing", atsDiagnostic("42703", `column "absent" of relation "t" does not exist`)},
			{"ALTER TABLE t SET TABLESPACE ats_missing, ALTER COLUMN absent TYPE INT", atsDiagnostic("42704", `tablespace "ats_missing" does not exist`)},
			{"ALTER TABLE t ALTER COLUMN note TYPE INT, SET TABLESPACE ats_missing", incompatibleType},
			{"ALTER TABLE t SET TABLESPACE ats_missing, ALTER COLUMN note TYPE INT", atsDiagnostic("42704", `tablespace "ats_missing" does not exist`)},
			{"ALTER TABLE t ALTER COLUMN id TYPE ats_missing_type, SET TABLESPACE ats_missing", missingType},
			{"ALTER TABLE t SET TABLESPACE ats_missing, ALTER COLUMN id TYPE ats_missing_type", atsDiagnostic("42704", `tablespace "ats_missing" does not exist`)},
			{"ALTER TABLE t ALTER COLUMN id TYPE BIGINT USING absent, SET TABLESPACE ats_missing", invalidUsing},
			{"ALTER TABLE t SET TABLESPACE ats_missing, ALTER COLUMN id TYPE BIGINT USING absent", atsDiagnostic("42704", `tablespace "ats_missing" does not exist`)},
			{"ALTER TABLE t ALTER COLUMN note TYPE INT USING note, SET TABLESPACE ats_missing", incompatibleUsing},
			{"ALTER TABLE t SET TABLESPACE ats_missing, ALTER COLUMN note TYPE INT USING note", atsDiagnostic("42704", `tablespace "ats_missing" does not exist`)},
			{"ALTER TABLE t ALTER COLUMN note TYPE TEXT COLLATE ats_missing_collation, SET TABLESPACE ats_missing", missingCollation},
			{"ALTER TABLE t SET TABLESPACE ats_missing, ALTER COLUMN note TYPE TEXT COLLATE ats_missing_collation", atsDiagnostic("42704", `tablespace "ats_missing" does not exist`)},
			{`ALTER TABLE t ALTER COLUMN id TYPE INT COLLATE "C", SET TABLESPACE ats_missing`, invalidCollation},
			{`ALTER TABLE t SET TABLESPACE ats_missing, ALTER COLUMN id TYPE INT COLLATE "C"`, atsDiagnostic("42704", `tablespace "ats_missing" does not exist`)},
			{"ALTER TABLE ats_precedence_mv SET TABLESPACE ats_missing, ADD COLUMN extra INT", atsDiagnostic("42704", `tablespace "ats_missing" does not exist`)},
			{"ALTER TABLE ats_precedence_mv ADD COLUMN extra INT, SET TABLESPACE ats_missing", wrongKind},
			{"ALTER TABLE t SET TABLESPACE pg_global, ALTER COLUMN absent TYPE INT", atsDiagnostic("42703", `column "absent" of relation "t" does not exist`)},
			{"ALTER TABLE t ALTER COLUMN absent TYPE INT, SET TABLESPACE pg_global", atsDiagnostic("42703", `column "absent" of relation "t" does not exist`)},
			{"ALTER TABLE t SET TABLESPACE pg_global, ADD COLUMN transient INT DEFAULT 3, ALTER COLUMN absent SET NOT NULL", atsDiagnostic("42703", `column "absent" of relation "t" does not exist`)},
			{"ALTER TABLE t SET TABLESPACE pg_global, ADD CONSTRAINT ats_bad CHECK(id<0)", badCheck},
			{"ALTER TABLE t ADD CONSTRAINT ats_bad CHECK(id<0), SET TABLESPACE pg_global", badCheck},
			{"ALTER TABLE t ADD COLUMN transient INT DEFAULT 3, SET TABLESPACE pg_global", atsDiagnostic("22023", "only shared relations can be placed in pg_global tablespace")},
			{"ALTER TABLE t SET TABLESPACE pg_global, ADD COLUMN transient INT DEFAULT 3", atsDiagnostic("22023", "only shared relations can be placed in pg_global tablespace")},
		} {
			t.Run(tc.q, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(tc.q, tc.want)
					s.Prepared = prepared
					s.ErrorStage = "execute"
					atsRun(t, p, b, c, s)
					atsRun(t, p, b, c, atsRows())
					atsRun(t, p, b, c, fixture.Select("SELECT id FROM ats_precedence_mv", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
					for _, name := range []string{"t", "ats_precedence_mv"} {
						atsPlacement(t, p, b, c, name, 0)
					}
					atsRun(t, p, b, c, fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='t'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"id"}, {"note"}}))
					atsRun(t, p, b, c, fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='ats_precedence_mv'::regclass::oid AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname"}, []string{"NAME"}, [][]any{{"id"}}))
					atsRun(t, p, b, c, fixture.Select("SELECT count(*) FROM pg_constraint WHERE conrelid='t'::regclass::oid AND conname='ats_bad'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				}
			})
		}
		// A complete preparation must be consumed, not just used as a preflight.
		// Use a fresh relation: the earlier fixture deliberately has a dependent MV.
		for _, clause := range []string{"ALTER COLUMN id TYPE BIGINT", "ALTER COLUMN id TYPE BIGINT USING id"} {
			for _, first := range []bool{true, false} {
				for _, prepared := range []bool{false, true} {
					atsExec(t, p, b, c, "CREATE TABLE ats_capture(id INT,note TEXT)")
					atsRun(t, p, b, c, fixture.Exec("INSERT INTO ats_capture VALUES(7,'keep')", 1))
					q := "ALTER TABLE ats_capture " + clause + ", SET TABLESPACE pg_default"
					if !first {
						q = "ALTER TABLE ats_capture SET TABLESPACE pg_default, " + clause
					}
					step := fixture.Exec(q, 0)
					step.Prepared = prepared
					atsRun(t, p, b, c, step)
					atsRun(t, p, b, c, fixture.Select("SELECT id,note FROM ats_capture", []string{"id", "note"}, []string{"INT8", "TEXT"}, [][]any{{int64(7), "keep"}}))
					atsPlacement(t, p, b, c, "ats_capture", 0)
					atsExec(t, p, b, c, "DROP TABLE ats_capture")
				}
			}
		}
	})
}

func TestReferenceAlterTablespaceBulkBuiltin(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsSeed(t, p, b, c)
		atsRoles(t, p, b, c)
		var notices []fixture.Diagnostic
		_, nc, nr := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		for _, suffix := range []string{"", " OWNED BY CURRENT_USER,CURRENT_ROLE,SESSION_USER", " OWNED BY ats_reader,ats_reader", " OWNED BY \"ats_other\""} {
			q := "ALTER TABLE ALL IN TABLESPACE pg_default" + suffix + " SET TABLESPACE pg_default NOWAIT"
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec(q, 0)
				s.Prepared = prepared
				fixture.RunStep(t, p.Ctx, nc, nr, s)
			}
		}
		if len(notices) != 0 {
			t.Fatalf("equal-space notices: %+v", notices)
		}
		for _, tc := range []struct{ q, code, message string }{
			{"ALTER TABLE ALL IN TABLESPACE ats_source OWNED BY ats_missing SET TABLESPACE ats_dest", "42704", `role "ats_missing" does not exist`},
			{"ALTER TABLE ALL IN TABLESPACE ats_source SET TABLESPACE ats_dest", "42704", `tablespace "ats_source" does not exist`},
			{"ALTER TABLE ALL IN TABLESPACE pg_default SET TABLESPACE ats_dest", "42704", `tablespace "ats_dest" does not exist`},
			{"ALTER TABLE ALL IN TABLESPACE pg_default OWNED BY public SET TABLESPACE pg_default", "42704", `role "public" does not exist`},
		} {
			atsError(t, p, b, c, tc.q, atsDiagnostic(tc.code, tc.message))
		}
		for _, pair := range [][2]string{{"pg_global", "pg_global"}, {"pg_global", "pg_default"}, {"pg_default", "pg_global"}} {
			atsError(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE "+pair[0]+" SET TABLESPACE "+pair[1]+" NOWAIT", atsDiagnostic("22023", "cannot move relations in to or out of pg_global tablespace"))
		}
		atsExec(t, p, b, c, "SET ROLE ats_reader")
		atsExec(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE pg_default SET TABLESPACE pg_default NOWAIT")
		atsExec(t, p, b, c, "RESET ROLE")
	})
}

func TestReferenceAlterTablespaceBulkLiveRoles(t *testing.T) {
	alterTablespaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		atsSeed(t, p, b, c)
		for _, role := range []string{"ats_live", "current_user"} {
			q := `ALTER TABLE ALL IN TABLESPACE pg_default OWNED BY "` + role + `" SET TABLESPACE pg_default NOWAIT`
			held, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("bulk Prepare resolved absent literal role", err)
			}
			defer held.Close()
			executor := fixture.HeldStatement{Executor: c, Statement: held, SQL: q}
			want := atsDiagnostic("42704", `role "`+role+`" does not exist`)
			atsRun(t, p, b, executor, fixture.ErrorDiagnostic(q, want))
			atsError(t, p, b, c, q, want)
			atsExec(t, p, b, c, `CREATE ROLE "`+role+`"`)
			atsRun(t, p, b, executor, fixture.Exec(q, 0))
			atsExec(t, p, b, c, q)
			atsRun(t, p, b, c, fixture.Prepared(fixture.Exec(q, 0)))
			atsExec(t, p, b, c, `DROP ROLE "`+role+`"`)
			atsRun(t, p, b, executor, fixture.ErrorDiagnostic(q, want))
			atsError(t, p, b, c, q, want)
		}
		// Unquoted CURRENT_USER resolves the existing identity, not that literal.
		atsExec(t, p, b, c, "ALTER TABLE ALL IN TABLESPACE pg_default OWNED BY CURRENT_USER SET TABLESPACE pg_default NOWAIT")
		atsPlacement(t, p, b, c, "t", 0)
		atsRun(t, p, b, c, atsRows())
	})
}
