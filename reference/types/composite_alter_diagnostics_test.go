package reference_test

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

func TestReferenceCompositeAlterStoredDependencies(t *testing.T) {
	for _, container := range []string{"direct", "array", "domain", "domain_array", "nested"} {
		for _, state := range []string{"empty", "null", "value"} {
			for _, action := range []string{"widen", "identity", "typmod", "collation"} {
				t.Run(container+"_"+state+"_"+action, func(t *testing.T) {
					fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
						fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note varchar(5))`)
						typ, input := "pair", "(7,x)"
						switch container {
						case "array":
							typ = "pair[]"
							input = `{"(7,x)"}`
						case "domain":
							fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN wrapped AS pair`)
							typ = "wrapped"
						case "domain_array":
							fixture.CompositeReferenceSetup(t, p, b, c, `CREATE DOMAIN wrapped AS pair[]`)
							typ = "wrapped"
							input = `{"(7,x)"}`
						case "nested":
							fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE wrapped AS (r pair)`)
							typ = "wrapped"
							input = `("(7,x)")`
						}
						fixture.CompositeReferenceSetup(t, p, b, c, "CREATE TABLE holder(v "+typ+")")
						want := [][]any{}
						if state == "null" {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES(NULL)`, 1))
							want = [][]any{{nil}}
						}
						if state == "value" {
							fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO holder VALUES($1)`, 1, input))
							want = [][]any{{input}}
						}
						clause := "id TYPE bigint"
						switch action {
						case "identity":
							clause = "id TYPE integer"
						case "typmod":
							clause = "note TYPE varchar(8)"
						case "collation":
							clause = `note TYPE varchar(5) COLLATE "C"`
						}
						for _, mode := range []string{"", " CASCADE"} {
							fixture.CompositeReferenceError(t, p, c, "ALTER TYPE pair ALTER ATTRIBUTE "+clause+mode, "execute", fixture.CompositeReferenceDiagnostic("0A000", `cannot alter type "pair" because column "holder.v" uses it`))
						}
						nativeType := ""
						if container == "array" || container == "domain_array" {
							nativeType = "_PAIR"
						}
						fixture.CompositeReferenceRows(t, p, b, c, `SELECT v FROM holder`, want, true, nativeType)
					})
				})
			}
		}
	}
	t.Run("index_consumer", func(t *testing.T) {
		fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`, `CREATE TABLE holder(id integer,note text)`)
			q := `CREATE INDEX holder_pair ON holder ((ROW(id,note)::pair))`
			if !b.PG {
				d := fixture.CompositeReferenceDiagnostic("42704", `data type public.pair has no default operator class for access method "btree"`)
				d.Hint = "You must specify an operator class for the index or define a default operator class for the data type."
				fixture.CompositeReferenceError(t, p, c, q, "execute", d)
				return
			}
			fixture.CompositeReferenceSetup(t, p, b, c, q)
			fixture.CompositeReferenceError(t, p, c, `ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint`, "execute", fixture.CompositeReferenceDiagnostic("0A000", `cannot alter type "pair" because column "holder_pair.row" uses it`))
		})
	})
}

func TestReferenceCompositeAlterDiagnostics(t *testing.T) {
	cases := []struct{ name, q, code, msg, token, stage string }{
		{"duplicate", `ALTER TYPE pair ADD ATTRIBUTE note text`, "42701", `column "note" of relation "pair" already exists`, "", "execute"},
		{"missing_drop", `ALTER TYPE pair DROP ATTRIBUTE absent`, "42703", `column "absent" of relation "pair" does not exist`, "", "execute"},
		{"missing_rename", `ALTER TYPE pair RENAME ATTRIBUTE absent TO label`, "42703", `column "absent" does not exist`, "", "execute"},
		{"missing_attribute", `/* é */ ALTER TYPE pair ALTER ATTRIBUTE absent TYPE bigint`, "42703", `column "absent" of relation "pair" does not exist`, "", "execute"},
		{"missing_source", `ALTER TYPE nosuchtype ADD ATTRIBUTE v text`, "42P01", `relation "nosuchtype" does not exist`, "", "execute"},
		{"noncollatable", `ALTER TYPE pair ALTER ATTRIBUTE id TYPE integer COLLATE "C"`, "42804", `collations are not supported by type integer`, "COLLATE", "execute"},
		{"missing_collation", `ALTER TYPE pair ALTER ATTRIBUTE note TYPE text COLLATE missing`, "42704", `collation "missing" for encoding "UTF8" does not exist`, "COLLATE", "execute"},
		{"unknown_type", `ALTER TYPE pair ADD ATTRIBUTE v missing`, "42704", `type "missing" does not exist`, "missing", "execute"},
		{"repeated", `ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint, ALTER ATTRIBUTE id TYPE numeric`, "0A000", `cannot alter type of column "id" twice`, "", "execute"},
		{"ordinary_target", `ALTER TYPE ordinary ADD ATTRIBUTE v text`, "42809", `"ordinary" is not a composite type`, "", "execute"},
		{"builtin_target", `ALTER TYPE integer ADD ATTRIBUTE v text`, "42P01", `relation "integer" does not exist`, "", "execute"},
		{"domain_target", `ALTER TYPE domain_target ADD ATTRIBUTE v text`, "42P01", `relation "domain_target" does not exist`, "", "execute"},
		{"enum_target", `ALTER TYPE enum_target ADD ATTRIBUTE v text`, "42P01", `relation "enum_target" does not exist`, "", "execute"},
		{"using", `ALTER TYPE pair ALTER ATTRIBUTE id TYPE bigint USING id::bigint`, "42601", `syntax error at or near "USING"`, "USING", "prepare"},
		{"collate_only", `ALTER TYPE pair ALTER ATTRIBUTE note COLLATE "C"`, "42601", `syntax error at or near "COLLATE"`, "COLLATE", "prepare"},
		{"add_if", `ALTER TYPE pair ADD ATTRIBUTE IF NOT EXISTS v text`, "42601", `syntax error at or near "NOT"`, "NOT", "prepare"},
		{"default", `ALTER TYPE pair ADD ATTRIBUTE v text DEFAULT 'x'`, "42601", `syntax error at or near "DEFAULT"`, "DEFAULT", "prepare"},
		{"constraint", `ALTER TYPE pair ADD ATTRIBUTE v integer CHECK(v>0)`, "42601", `syntax error at or near "CHECK"`, "CHECK", "prepare"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE pair AS (id integer,note text)`, `CREATE TABLE ordinary(id integer)`, `CREATE DOMAIN domain_target AS integer`, `CREATE TYPE enum_target AS ENUM ('ok')`)
				d := fixture.CompositeReferenceDiagnostic(tc.code, tc.msg)
				if tc.token != "" {
					d.Position = strconv.Itoa(utf8.RuneCountInString(tc.q[:strings.Index(tc.q, tc.token)]) + 1)
				}
				fixture.CompositeReferenceError(t, p, c, tc.q, tc.stage, d)
				step := fixture.Prepared(fixture.Select(`SELECT '(7,x)'::pair AS v`, []string{"v"}, []string{""}, [][]any{{"(7,x)"}}))
				step.CheckMetadata = fixture.EnumReferenceMetadata(b.PG, false)
				fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, step)
			})
		})
	}
	t.Run("notices_and_field_constraint", func(t *testing.T) {
		fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
			var notices []fixture.Diagnostic
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, &notices)
			b.Recorder = rec
			fixture.CompositeReferenceSetup(t, p, b, c, `CREATE TYPE test_typex AS (a integer,b text)`, `CREATE TABLE test_tblx(y test_typex CHECK ((y).a>0))`)
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec(`INSERT INTO test_tblx VALUES('(7,x)')`, 1))
			notices = nil
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Prepared(fixture.Exec(`ALTER TYPE test_typex DROP ATTRIBUTE IF EXISTS c`, 0)))
			fixture.DomainReferenceNotice(t, &notices, `column "c" of relation "test_typex" does not exist, skipping`, "")
			notices = nil
			d := fixture.CompositeReferenceDiagnostic("2BP01", `cannot drop column a of composite type test_typex because other objects depend on it`)
			d.Detail = `constraint test_tblx_y_check on table test_tblx depends on column a of composite type test_typex`
			d.Hint = `Use DROP ... CASCADE to drop the dependent objects too.`
			fixture.CompositeReferenceError(t, p, c, `ALTER TYPE test_typex DROP ATTRIBUTE a RESTRICT`, "execute", d)
			fixture.CompositeReferenceRows(t, p, b, c, `SELECT y AS v FROM test_tblx`, [][]any{{"(7,x)"}}, true)
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec(`ALTER TYPE test_typex DROP ATTRIBUTE a CASCADE`, 0))
			fixture.DomainReferenceNotice(t, &notices, `drop cascades to constraint test_tblx_y_check on table test_tblx`, "")
			fixture.CompositeReferenceRows(t, p, b, c, `SELECT y AS v FROM test_tblx`, [][]any{{"(x)"}}, true)
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Exec(`INSERT INTO test_tblx VALUES('(unconstrained)')`, 1))
			fixture.RunStep(t, p.Ctx, c, rec, fixture.Select(`SELECT count(*) AS n FROM pg_constraint WHERE conrelid='test_tblx'::regclass::oid`, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		})
	})
}

func TestReferenceCompositeAlterAuthorization(t *testing.T) {
	for _, action := range []string{`ADD ATTRIBUTE flag boolean`, `RENAME ATTRIBUTE note TO label`, `ALTER ATTRIBUTE id TYPE bigint`} {
		t.Run(strings.Fields(action)[0], func(t *testing.T) {
			role := "composite_alter_actor"
			fixture.RoleReferenceCleanup(t, []string{role})
			fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				defer c.ExecContext(context.Background(), `RESET SESSION AUTHORIZATION`)
				fixture.CompositeReferenceSetup(t, p, b, c, "CREATE USER "+role, `CREATE TYPE pair AS (id integer,note text)`)
				if b.PG {
					fixture.CompositeReferenceSetup(t, p, b, c, "GRANT USAGE ON SCHEMA public TO "+role)
				}
				fixture.CompositeReferenceSetup(t, p, b, c, "SET SESSION AUTHORIZATION "+role)
				fixture.CompositeReferenceError(t, p, c, "ALTER TYPE public.pair "+action, "execute", fixture.CompositeReferenceDiagnostic("42501", "must be owner of table pair"))
				fixture.CompositeReferenceSetup(t, p, b, c, `RESET SESSION AUTHORIZATION`)
				step := fixture.Prepared(fixture.Select(`SELECT '(7,x)'::pair AS v`, []string{"v"}, []string{""}, [][]any{{"(7,x)"}}))
				step.CheckMetadata = fixture.EnumReferenceMetadata(b.PG, false)
				fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, step)
				if b.PG {
					fixture.CompositeReferenceSetup(t, p, b, c, "REVOKE USAGE ON SCHEMA public FROM "+role)
				}
				fixture.CompositeReferenceSetup(t, p, b, c, "DROP ROLE "+role)
			})
		})
	}
	t.Run("new_type_usage", func(t *testing.T) {
		role := "composite_usage_actor"
		fixture.RoleReferenceCleanup(t, []string{role})
		fixture.CompositeReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer c.ExecContext(context.Background(), `RESET SESSION AUTHORIZATION`)
			fixture.CompositeReferenceSetup(t, p, b, c, "CREATE USER "+role, `CREATE TYPE restricted AS (v integer)`, `CREATE TYPE pair AS (id integer,note text)`, "ALTER TYPE pair OWNER TO "+role)
			if !b.PG {
				return
			} // Existing native SQL lacks REVOKE TYPE; production package authority units cover that entrance.
			fixture.CompositeReferenceSetup(t, p, b, c, "GRANT USAGE ON SCHEMA public TO "+role, `REVOKE USAGE ON TYPE restricted FROM PUBLIC`, "SET SESSION AUTHORIZATION "+role)
			for _, q := range []string{`ALTER TYPE pair ADD ATTRIBUTE extra restricted`, `ALTER TYPE pair ALTER ATTRIBUTE id TYPE restricted`} {
				fixture.CompositeReferenceError(t, p, c, q, "execute", fixture.CompositeReferenceDiagnostic("42501", "permission denied for type restricted"))
			}
			fixture.CompositeReferenceSetup(t, p, b, c, `RESET SESSION AUTHORIZATION`)
			step := fixture.Prepared(fixture.Select(`SELECT '(7,x)'::pair AS v`, []string{"v"}, []string{""}, [][]any{{"(7,x)"}}))
			step.CheckMetadata = fixture.EnumReferenceMetadata(b.PG, false)
			fixture.EnumReferenceRunStep(t, p.Ctx, c, b.Recorder, step)
			fixture.CompositeReferenceSetup(t, p, b, c, `DROP TYPE pair`, "REVOKE USAGE ON SCHEMA public FROM "+role, "DROP ROLE "+role)
		})
	})
}
