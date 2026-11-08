package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceAdminCommentObjectKindsDiagnostics pins the exact runtime
// diagnostics for the new COMMENT kinds: missing and wrong-kind targets,
// schema USAGE, routine ambiguity and syntax, and owner-only authorization.
// Every run-time error has no Position; the one syntax error is the parser's.
func TestReferenceAdminCommentObjectKindsDiagnostics(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cok_owner_direct", "cok_other_direct", "cok_owner_prepared", "cok_other_prepared"})
	fixture.SchemaReferenceCleanup(t, "hidden")
	cokScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, s *cokSession) {
		owner, other := s.role("cok_owner"), s.role("cok_other")
		for _, q := range []string{
			"CREATE TABLE t2(v INT)",
			"CREATE TYPE e2 AS ENUM ('a')",
			"CREATE TABLE cok_part(v INT) PARTITION BY RANGE(v)",
			"CREATE TABLE child4 PARTITION OF cok_part FOR VALUES FROM (0) TO (10)",
			"CREATE FUNCTION f2(a INT) RETURNS INT LANGUAGE SQL AS 'SELECT $1'",
			"CREATE FUNCTION f2(a TEXT) RETURNS TEXT LANGUAGE SQL AS 'SELECT $1'",
			"CREATE PROCEDURE p2(a INT) LANGUAGE SQL AS 'SELECT 1'",
			"CREATE USER " + owner,
			"CREATE USER " + other,
			"CREATE SCHEMA hidden",
			"CREATE TYPE hidden.hidden_type AS ENUM ('a')",
			"CREATE TYPE hidden.ht AS ENUM ('a')",
		} {
			s.exec(q)
		}

		for _, tc := range []struct{ query, code, message string }{
			{"COMMENT ON TYPE nosuch IS NULL", "42704", `type "nosuch" does not exist`},
			{"COMMENT ON DOMAIN nosuchdom IS NULL", "42704", `type "nosuchdom" does not exist`},
			{"COMMENT ON DOMAIN e2 IS NULL", "42809", `"e2" is not a domain`},
			{"COMMENT ON TYPE missing_schema.t IS NULL", "3F000", `schema "missing_schema" does not exist`},
			{"COMMENT ON INDEX nosuchix IS NULL", "42P01", `relation "nosuchix" does not exist`},
			{"COMMENT ON INDEX nosuchschema.nosuchix IS NULL", "3F000", `schema "nosuchschema" does not exist`},
			{"COMMENT ON INDEX t2 IS NULL", "42809", `"t2" is not an index`},
			{"COMMENT ON TRIGGER nosuchtr ON t2 IS NULL", "42704", `trigger "nosuchtr" for table "t2" does not exist`},
			{"COMMENT ON TRIGGER tr2 ON nosuchtbl IS NULL", "42P01", `relation "nosuchtbl" does not exist`},
			{"COMMENT ON TRIGGER nope ON public.child4 IS NULL", "42704", `trigger "nope" for table "child4" does not exist`},
			{"COMMENT ON FUNCTION nosuchfn(int) IS NULL", "42883", `function nosuchfn(integer) does not exist`},
			{"COMMENT ON FUNCTION nosuchfn IS NULL", "42883", `could not find a function named "nosuchfn"`},
			{"COMMENT ON FUNCTION f2(bpchar, integer) IS NULL", "42883", `function f2(character, integer) does not exist`},
			{"COMMENT ON PROCEDURE f2(int) IS NULL", "42809", `f2(integer) is not a procedure`},
			{"COMMENT ON FUNCTION p2(int) IS NULL", "42809", `p2(integer) is not a function`},
			// OUT arguments are ignored: the lookup is p3() and it is absent.
			{"COMMENT ON PROCEDURE p3(OUT int) IS NULL", "42883", `procedure p3() does not exist`},
			{"COMMENT ON ROUTINE nosuchrt(int) IS NULL", "42883", `function nosuchrt(integer) does not exist`},
			{"COMMENT ON ROUTINE nosuchrt IS NULL", "42883", `could not find a function named "nosuchrt"`},
		} {
			s.denied(tc.query, tc.code, tc.message)
		}
		s.failure("COMMENT ON FUNCTION f2 IS NULL", fixture.Diagnostic{
			Code:    "42725",
			Message: `function name "f2" is not unique`,
			Hint:    "Specify the argument list to select the function unambiguously.",
		}, "execute")
		s.failure("COMMENT ON TYPE t IS 7", fixture.Diagnostic{
			Code:     "42601",
			Message:  `syntax error at or near "7"`,
			Position: "22",
		}, "prepare")

		// A visible qualified type in a schema without USAGE is denied; the
		// implicit search path simply omits that schema, so the unqualified
		// name is missing rather than denied.
		s.execDirect("SET SESSION AUTHORIZATION "+other)
		s.execDirect("SET search_path = hidden, public")
		s.denied("COMMENT ON TYPE hidden.hidden_type IS NULL", "42501", "permission denied for schema hidden")
		s.denied("COMMENT ON TYPE ht IS NULL", "42704", `type "ht" does not exist`)
		s.execDirect("SET search_path = public")
		s.execDirect("RESET SESSION AUTHORIZATION")

		// The owner can comment every new kind; the exact owner-only texts are
		// then pinned for a non-owner and the text is unchanged afterwards.
		s.execDirect("SET SESSION AUTHORIZATION "+owner)
		fixture.WithPublicSchemaOwner(t, p, b, owner, func() {
			for _, q := range []string{
				"CREATE TYPE e3 AS ENUM ('a')",
				"CREATE DOMAIN d3 AS INT",
				"CREATE TABLE t3(v INT)",
				"CREATE INDEX ix3 ON t3(v)",
				"CREATE FUNCTION tr3_fn() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
				"CREATE TRIGGER tr3 BEFORE INSERT ON t3 FOR EACH ROW EXECUTE FUNCTION tr3_fn()",
				"CREATE FUNCTION f3(a INT) RETURNS INT LANGUAGE SQL AS 'SELECT $1'",
				"CREATE PROCEDURE p3(a INT) LANGUAGE SQL AS 'SELECT 1'",
				"COMMENT ON TYPE e3 IS 'owned type'",
				"COMMENT ON DOMAIN d3 IS 'owned domain'",
				"COMMENT ON INDEX ix3 IS 'owned index'",
				"COMMENT ON TRIGGER tr3 ON t3 IS 'owned trigger'",
				"COMMENT ON FUNCTION f3(int) IS 'owned function'",
				"COMMENT ON PROCEDURE p3(int) IS 'owned procedure'",
			} {
				s.exec(q)
			}
		})
		s.execDirect("SET SESSION AUTHORIZATION "+other)
		for _, tc := range []struct{ query, code, message string }{
			{"COMMENT ON TYPE e3 IS 'bad'", "42501", `must be owner of type e3`},
			{"COMMENT ON DOMAIN d3 IS 'bad'", "42501", `must be owner of type d3`},
			{"COMMENT ON INDEX ix3 IS 'bad'", "42501", `must be owner of index ix3`},
			{"COMMENT ON INDEX public.ix3 IS 'bad'", "42501", `must be owner of index ix3`},
			{"COMMENT ON TRIGGER tr3 ON t3 IS 'bad'", "42501", `must be owner of relation t3`},
			{"COMMENT ON FUNCTION f3(int) IS 'bad'", "42501", `must be owner of function f3`},
			{"COMMENT ON PROCEDURE p3(int) IS 'bad'", "42501", `must be owner of procedure p3`},
			{"COMMENT ON ROUTINE f3(int) IS 'bad'", "42501", `must be owner of routine f3`},
			{"COMMENT ON ROUTINE p3(int) IS 'bad'", "42501", `must be owner of routine p3`},
			// Missing trigger resolution precedes the ownership check.
			{"COMMENT ON TRIGGER nope ON t3 IS NULL", "42704", `trigger "nope" for table "t3" does not exist`},
		} {
			s.denied(tc.query, tc.code, tc.message)
		}
		s.run(cokText("SELECT obj_description('e3'::regtype,'pg_type') AS text", "owned type"))
		s.run(cokText("SELECT obj_description('d3'::regtype,'pg_type') AS text", "owned domain"))
		s.run(cokText("SELECT obj_description('ix3'::regclass,'pg_class') AS text", "owned index"))
		s.run(cokText("SELECT obj_description(oid,'pg_trigger') AS text FROM pg_catalog.pg_trigger WHERE tgrelid='t3'::regclass AND tgname='tr3'", "owned trigger"))
		s.run(cokText("SELECT obj_description('f3(integer)'::regprocedure,'pg_proc') AS text", "owned function"))
		s.run(cokText("SELECT obj_description('p3(integer)'::regprocedure,'pg_proc') AS text", "owned procedure"))

		// The superuser administrator can replace every one of those comments.
		s.execDirect("RESET SESSION AUTHORIZATION")
		for _, tc := range []struct{ comment, query, want string }{
			{"COMMENT ON TYPE e3 IS 'super type'", "SELECT obj_description('e3'::regtype,'pg_type') AS text", "super type"},
			{"COMMENT ON DOMAIN d3 IS 'super domain'", "SELECT obj_description('d3'::regtype,'pg_type') AS text", "super domain"},
			{"COMMENT ON INDEX ix3 IS 'super index'", "SELECT obj_description('ix3'::regclass,'pg_class') AS text", "super index"},
			{"COMMENT ON TRIGGER tr3 ON t3 IS 'super trigger'", "SELECT obj_description(oid,'pg_trigger') AS text FROM pg_catalog.pg_trigger WHERE tgrelid='t3'::regclass AND tgname='tr3'", "super trigger"},
			{"COMMENT ON FUNCTION f3(int) IS 'super function'", "SELECT obj_description('f3(integer)'::regprocedure,'pg_proc') AS text", "super function"},
			{"COMMENT ON PROCEDURE p3(int) IS 'super procedure'", "SELECT obj_description('p3(integer)'::regprocedure,'pg_proc') AS text", "super procedure"},
			{"COMMENT ON ROUTINE f3(int) IS 'super routine'", "SELECT obj_description('f3(integer)'::regprocedure,'pg_proc') AS text", "super routine"},
			{"COMMENT ON ROUTINE p3(int) IS 'super routine procedure'", "SELECT obj_description('p3(integer)'::regprocedure,'pg_proc') AS text", "super routine procedure"},
		} {
			s.exec(tc.comment)
			s.run(cokText(tc.query, tc.want))
		}

		// These are local objects: pg_shdescription never gains their classoids.
		s.run(fixture.Select("SELECT count(*) FROM pg_catalog.pg_shdescription WHERE classoid IN (1247,1255,1259,2620)", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		s.exec("DROP SCHEMA hidden CASCADE")
	})
}