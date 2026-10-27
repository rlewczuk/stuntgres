package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRoutineAttributesDeclarationsAndMetadata(t *testing.T) {
	t.Run("defaults_and_explicit", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c,
				"CREATE FUNCTION ra_d_quote(integer) RETURNS integer LANGUAGE SQL AS 'SELECT $1'",
				"CREATE FUNCTION ra_d_standard(integer) RETURNS integer LANGUAGE SQL RETURN $1",
				"CREATE FUNCTION ra_d_pl(integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN $1; END$$",
				"CREATE FUNCTION ra_d_set(integer) RETURNS SETOF integer LANGUAGE SQL AS 'SELECT $1'",
				"CREATE FUNCTION ra_d_explicit(integer) RETURNS SETOF integer LANGUAGE SQL IMMUTABLE STRICT SECURITY DEFINER PARALLEL RESTRICTED COST 7 ROWS 12 SET application_name='first' SET search_path=public SET application_name='last' AS 'SELECT $1'",
				"CREATE FUNCTION ra_d_safe(integer) RETURNS integer LANGUAGE SQL STABLE PARALLEL SAFE AS 'SELECT $1'",
				"CREATE FUNCTION ra_d_unsafe(integer) RETURNS integer LANGUAGE SQL VOLATILE PARALLEL UNSAFE AS 'SELECT $1'")
			q := "SELECT proname::text AS name,provolatile,proisstrict,prosecdef,proparallel,procost,prorows,proconfig FROM pg_proc WHERE proname IN ('ra_d_quote','ra_d_standard','ra_d_pl','ra_d_set','ra_d_explicit','ra_d_safe','ra_d_unsafe') ORDER BY proname"
			attributesDeclarationCatalogBoth(t, p, b, c, q,
				[]string{"name", "provolatile", "proisstrict", "prosecdef", "proparallel", "procost", "prorows", "proconfig"},
				[]string{"TEXT", "CHAR", "BOOL", "BOOL", "CHAR", "FLOAT4", "FLOAT4", "_TEXT"},
				[][]any{
					{"ra_d_explicit", "i", true, true, "r", float64(7), float64(12), "{application_name=last,search_path=public}"},
					{"ra_d_pl", "v", false, false, "u", float64(100), float64(0), nil},
					{"ra_d_quote", "v", false, false, "u", float64(100), float64(0), nil},
					{"ra_d_safe", "s", false, false, "s", float64(100), float64(0), nil},
					{"ra_d_set", "v", false, false, "u", float64(100), float64(1000), nil},
					{"ra_d_standard", "v", false, false, "u", float64(100), float64(0), nil},
					{"ra_d_unsafe", "v", false, false, "u", float64(100), float64(0), nil},
				})
			resultsBothMetadata(t, p, b, c,
				"SELECT routine_name::text AS name,is_null_call::text AS null_call,is_deterministic::text AS deterministic,security_type::text AS security FROM information_schema.routines WHERE routine_schema='public' AND routine_name IN ('ra_d_explicit','ra_d_quote') ORDER BY routine_name",
				[]string{"name", "null_call", "deterministic", "security"}, []string{"TEXT", "TEXT", "TEXT", "TEXT"},
				[][]any{{"ra_d_explicit", "YES", "YES", "DEFINER"}, {"ra_d_quote", "NO", "NO", "INVOKER"}},
				[]resultsNativeColumn{resultsTextMetadata, resultsTextMetadata, resultsTextMetadata, resultsTextMetadata})
			// These are released pg_proc attribute identities, not condensed native
			// row indexes. The supplemental declaration is a real pinned builtin.
			attributesBoth(t, p, b, c,
				"SELECT attname::text AS name,attnum::integer AS number,atttypid::regtype::text AS type,attndims::integer AS dimensions,coalesce(attnotnull,false) AS required FROM pg_attribute WHERE attrelid='pg_proc'::regclass AND attname IN ('prokind','prosecdef','proisstrict','provolatile','proparallel','procost','prorows','proconfig') ORDER BY attnum",
				[]string{"name", "number", "type", "dimensions", "required"}, []string{"TEXT", "INT4", "TEXT", "INT4", "BOOL"},
				[][]any{{"procost", int64(6), "real", int64(0), true}, {"prorows", int64(7), "real", int64(0), true}, {"prokind", int64(10), "\"char\"", int64(0), true}, {"prosecdef", int64(11), "boolean", int64(0), true}, {"proisstrict", int64(13), "boolean", int64(0), true}, {"provolatile", int64(15), "\"char\"", int64(0), true}, {"proparallel", int64(16), "\"char\"", int64(0), true}, {"proconfig", int64(29), "text[]", int64(1), false}})
			attributesDeclarationCatalogBoth(t, p, b, c, "SELECT prosecdef,proconfig FROM pg_proc WHERE oid=40::oid", []string{"prosecdef", "proconfig"}, []string{"BOOL", "_TEXT"}, [][]any{{false, nil}})
		})
	})
	t.Run("prepare_is_producer_free", func(t *testing.T) {
		for _, prepared := range []bool{false, true} {
			proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				resultsExec(t, p, b, c, "CREATE TABLE ra_d_effect(v integer)", "SET application_name='outside'")
				q := "CREATE FUNCTION ra_d_pending(integer) RETURNS integer LANGUAGE SQL STRICT SECURITY DEFINER PARALLEL SAFE COST 9 SET application_name='inside' AS 'INSERT INTO ra_d_effect VALUES($1); SELECT 7'"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				proceduresCount(t, p, b, c, "pg_proc WHERE proname='ra_d_pending'", 0)
				proceduresCount(t, p, b, c, "ra_d_effect", 0)
				resultsBothMetadata(t, p, b, c, "SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"outside"}}, []resultsNativeColumn{{Length: 1<<63 - 1, LengthKnown: true, NullableKnown: true}})
				if prepared {
					fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, fixture.Exec(q, 0))
				} else {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
				}
				proceduresCount(t, p, b, c, "ra_d_effect", 0)
				resultsBothMetadata(t, p, b, c, "SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"outside"}}, []resultsNativeColumn{{Length: 1<<63 - 1, LengthKnown: true, NullableKnown: true}})
				proceduresBoth(t, p, b, c, "SELECT ra_d_pending(1) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}})
				proceduresCount(t, p, b, c, "ra_d_effect", 2)
			})
		}
	})
	t.Run("procedure_legal_options", func(t *testing.T) {
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c,
				"SET application_name='outside'",
				"CREATE PROCEDURE ra_d_psql(OUT answer text) LANGUAGE SQL SECURITY DEFINER SET application_name='inside' AS $$SELECT current_setting('application_name')$$",
				"CREATE PROCEDURE ra_d_ppl(OUT answer text) LANGUAGE plpgsql SECURITY DEFINER SET application_name='inside' AS $$BEGIN answer := current_setting('application_name'); END$$")
			for _, name := range []string{"ra_d_psql", "ra_d_ppl"} {
				proceduresBoth(t, p, b, c, "CALL "+name+"(NULL)", []string{"answer"}, []string{"TEXT"}, [][]any{{"inside"}})
			}
			attributesDeclarationCatalogBoth(t, p, b, c, "SELECT proname::text AS name,prokind,provolatile,proisstrict,prosecdef,proparallel,procost,prorows,proconfig FROM pg_proc WHERE proname IN ('ra_d_psql','ra_d_ppl') ORDER BY proname", []string{"name", "prokind", "provolatile", "proisstrict", "prosecdef", "proparallel", "procost", "prorows", "proconfig"}, []string{"TEXT", "CHAR", "CHAR", "BOOL", "BOOL", "CHAR", "FLOAT4", "FLOAT4", "_TEXT"}, [][]any{{"ra_d_ppl", "p", "v", false, true, "u", float64(100), float64(0), "{application_name=inside}"}, {"ra_d_psql", "p", "v", false, true, "u", float64(100), float64(0), "{application_name=inside}"}})
			resultsBothMetadata(t, p, b, c, "SHOW application_name", []string{"application_name"}, []string{"TEXT"}, [][]any{{"outside"}}, []resultsNativeColumn{{Length: 1<<63 - 1, LengthKnown: true, NullableKnown: true}})
		})
	})
	t.Run("information_schema_privilege_filter", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"ra_d_reader"})
		proceduresScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			resultsExec(t, p, b, c, "CREATE ROLE ra_d_reader", "CREATE FUNCTION ra_d_visible() RETURNS integer LANGUAGE SQL IMMUTABLE STRICT SECURITY DEFINER AS 'SELECT 7'", "CREATE FUNCTION ra_d_hidden() RETURNS integer LANGUAGE SQL AS 'SELECT 9'", "REVOKE ALL ON FUNCTION ra_d_visible(),ra_d_hidden() FROM PUBLIC", "GRANT EXECUTE ON FUNCTION ra_d_visible() TO ra_d_reader", "SET SESSION AUTHORIZATION ra_d_reader")
			defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			attributesBoth(t, p, b, c, "SELECT routine_name::text AS name,is_null_call::text AS null_call,is_deterministic::text AS deterministic,security_type::text AS security,routine_definition::text AS body FROM information_schema.routines WHERE routine_schema='public' AND routine_name IN ('ra_d_visible','ra_d_hidden') ORDER BY routine_name", []string{"name", "null_call", "deterministic", "security", "body"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{"ra_d_visible", "YES", "YES", "DEFINER", nil}})
			resultsExec(t, p, b, c, "RESET SESSION AUTHORIZATION")
		})
	})
}

// This checker is only for the explicitly named pg_proc fields used below.
// Their native stored-source nullability differs from expression descriptors;
// pq reports unknown nullability for both.
func attributesDeclarationCatalogBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, q string, names, types []string, values [][]any) {
	t.Helper()
	profile := make([]resultsNativeColumn, len(names))
	for i, name := range names {
		switch name {
		case "name":
			profile[i] = resultsTextMetadata
		case "proconfig":
			profile[i] = resultsNullableMetadata
		case "prokind", "provolatile", "proisstrict", "prosecdef", "proparallel", "procost", "prorows":
			profile[i] = resultsNonNullMetadata
		default:
			t.Fatalf("missing independent pg_proc profile for %q", name)
		}
	}
	attributesBothMetadata(t, p, b, c, q, names, types, values, profile)
}
