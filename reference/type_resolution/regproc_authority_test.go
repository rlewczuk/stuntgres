package reference_test

import (
	"database/sql"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRegProcSearchPath(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"regproc_reader"})
	fixture.SchemaReferenceCleanup(t, "regproc_provider", "regproc_moved")
	regprocScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, q := range []string{"CREATE SCHEMA regproc_provider", "CREATE EXTENSION unaccent SCHEMA regproc_provider", "SET search_path TO regproc_provider,public"} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
		}
		regprocBoth(t, p, c, r, fixture.Select("SELECT 'boolin'::regproc AS r,'unaccent(text)'::regprocedure AS p", []string{"r", "p"}, []string{"REGPROC", "REGPROCEDURE"}, [][]any{{"boolin", "unaccent(text)"}}))
		// The real provider has two unaccent signatures: qualification does not
		// make its bare regproc name unique. Numeric output still names the row.
		_, err := c.ExecContext(p.Ctx, "SELECT $1::regproc", "regproc_provider.unaccent")
		regprocError(t, err, "42725", `more than one function named "regproc_provider.unaccent"`, "SELECT $1::regproc", "direct-frame", "regproc_provider.unaccent")
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'unaccent(text)'::regprocedure::oid::bigint").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO public", 0))
		regprocBoth(t, p, c, r, fixture.Select("SELECT $1::oid::regproc AS r,$1::oid::regprocedure AS p", []string{"r", "p"}, []string{"REGPROC", "REGPROCEDURE"}, [][]any{{"regproc_provider.unaccent", "regproc_provider.unaccent(text)"}}, oid))
		_, err = c.ExecContext(p.Ctx, "SELECT $1::regprocedure", "unaccent(text)")
		regprocError(t, err, "42883", `function "unaccent(text)" does not exist`, "SELECT $1::regprocedure", "direct-frame", "unaccent(text)")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("ALTER SCHEMA regproc_provider RENAME TO regproc_moved", 0))
		regprocBoth(t, p, c, r, fixture.Select("SELECT $1::oid::regprocedure AS p", []string{"p"}, []string{"REGPROCEDURE"}, [][]any{{"regproc_moved.unaccent(text)"}}, oid))
		for _, q := range []string{"CREATE ROLE regproc_reader", "SET ROLE regproc_reader"} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
		}
		_, err = c.ExecContext(p.Ctx, "SELECT $1::regprocedure", "regproc_moved.unaccent(text)")
		regprocError(t, err, "42501", "permission denied for schema regproc_moved", "SELECT $1::regprocedure", "direct-frame", "regproc_moved.unaccent(text)")
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("RESET ROLE", 0))
		recovery := "GRANT USAGE ON SCHEMA regproc_moved TO regproc_reader"
		if !b.PG {
			// Existing native schema ACL SQL boundary: transfer namespace
			// ownership, not provider ownership, to establish real visibility.
			recovery = "ALTER SCHEMA regproc_moved OWNER TO regproc_reader"
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(recovery, 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET ROLE regproc_reader", 0))
		regprocBoth(t, p, c, r, fixture.Select("SELECT 'regproc_moved.unaccent(text)'::regprocedure::oid::bigint AS id", []string{"id"}, []string{"INT8"}, [][]any{{oid}}))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("RESET ROLE", 0))
	})
}

func TestReferenceRegProcPreparedAuthority(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"regproc_held_reader"})
	fixture.SchemaReferenceCleanup(t, "regproc_held")
	regprocScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder) {
		for _, q := range []string{"CREATE SCHEMA regproc_held", "CREATE EXTENSION unaccent SCHEMA regproc_held", "SET search_path TO regproc_held,public"} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
		}
		const literal = "SELECT 'unaccent(text)'::regprocedure::oid::bigint AS id"
		const frameQ = "SELECT $1::regprocedure::oid::bigint AS id"
		const runtimeQ = "SELECT $1::text::regprocedure::oid::bigint AS id"
		held, err := c.PrepareContext(p.Ctx, literal)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		frame, err := c.PrepareContext(p.Ctx, frameQ)
		if err != nil {
			t.Fatal(err)
		}
		defer frame.Close()
		runtime, err := c.PrepareContext(p.Ctx, runtimeQ)
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		var oid int64
		if err := held.QueryRowContext(p.Ctx).Scan(&oid); err != nil {
			t.Fatal(err)
		}
		if oid <= 0 {
			t.Fatal("invalid installed identity", oid)
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE TABLE public.regproc_data_only(p regprocedure)", 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("INSERT INTO public.regproc_data_only VALUES('unaccent(text)')", 1))
		replacement := oid
		if b.PG {
			// Native DROP EXTENSION is an adjacent, unsupported utility. Keep
			// PostgreSQL's complete referent-only drop/reinstall contract here.
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("DROP EXTENSION unaccent", 0))
			regprocBoth(t, p, c, r, fixture.Select("SELECT p,p::oid::bigint AS id FROM public.regproc_data_only", []string{"p", "id"}, []string{"REGPROCEDURE", "INT8"}, [][]any{{strconv.FormatInt(oid, 10), oid}}))
			regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: held, SQL: literal}, r, fixture.Select(literal, []string{"id"}, []string{"INT8"}, [][]any{{oid}}))
			_, err = c.ExecContext(p.Ctx, literal)
			regprocError(t, err, "42883", `function "unaccent(text)" does not exist`, literal, "literal-query", "unaccent(text)")
			for _, fact := range []struct {
				q, route string
				stmt     *sql.Stmt
			}{{frameQ, "held-frame", frame}, {runtimeQ, "held-runtime-text", runtime}} {
				rows, err := fact.stmt.QueryContext(p.Ctx, "unaccent(text)")
				if rows != nil {
					rows.Close()
					t.Error("missing held referent returned cursor")
				}
				regprocError(t, err, "42883", `function "unaccent(text)" does not exist`, fact.q, fact.route, "unaccent(text)")
			}
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("CREATE EXTENSION unaccent SCHEMA regproc_held", 0))
			if err := c.QueryRowContext(p.Ctx, literal).Scan(&replacement); err != nil {
				t.Fatal(err)
			}
			if replacement == oid || replacement <= 0 {
				t.Fatal("reinstall reused old identity", oid, replacement)
			}
		} else {
			want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported")
			want.Position = "1"
			fixture.RunStep(t, p.Ctx, c, r, fixture.ErrorDiagnostic("DROP EXTENSION unaccent", want))
			regprocBoth(t, p, c, r, fixture.Select("SELECT p,p::oid::bigint AS id FROM public.regproc_data_only", []string{"p", "id"}, []string{"REGPROCEDURE", "INT8"}, [][]any{{"unaccent(text)", oid}}))
			regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: held, SQL: literal}, r, fixture.Select(literal, []string{"id"}, []string{"INT8"}, [][]any{{oid}}))
			regprocBoth(t, p, c, r, fixture.Select(literal, []string{"id"}, []string{"INT8"}, [][]any{{oid}}))
		}
		for _, fact := range []struct {
			q    string
			stmt *sql.Stmt
		}{{frameQ, frame}, {runtimeQ, runtime}} {
			regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: fact.stmt, SQL: fact.q}, r, fixture.Select(fact.q, []string{"id"}, []string{"INT8"}, [][]any{{replacement}}, "unaccent(text)"))
			regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: fact.stmt, SQL: fact.q}, r, fixture.Select(fact.q, []string{"id"}, []string{"INT8"}, [][]any{{nil}}, nil))
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO public", 0))
		rows, err := held.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
			t.Error("path-invalidated literal returned cursor")
		}
		regprocError(t, err, "42883", `function "unaccent(text)" does not exist`, literal, "held-literal", "")
		for _, fact := range []struct {
			q, route string
			stmt     *sql.Stmt
		}{{frameQ, "held-frame", frame}, {runtimeQ, "held-runtime-text", runtime}} {
			rows, err := fact.stmt.QueryContext(p.Ctx, "unaccent(text)")
			if rows != nil {
				rows.Close()
				t.Error("path-invalidated input returned cursor")
			}
			regprocError(t, err, "42883", `function "unaccent(text)" does not exist`, fact.q, fact.route, "unaccent(text)")
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET search_path TO regproc_held,public", 0))
		regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: held, SQL: literal}, r, fixture.Select(literal, []string{"id"}, []string{"INT8"}, [][]any{{replacement}}))
		for _, q := range []string{"CREATE ROLE regproc_held_reader", "SET ROLE regproc_held_reader"} {
			fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(q, 0))
		}
		for _, fact := range []struct {
			q, route string
			stmt     *sql.Stmt
			args     []any
		}{{literal, "held-literal", held, nil}, {frameQ, "held-frame", frame, []any{"unaccent(text)"}}, {runtimeQ, "held-runtime-text", runtime, []any{"unaccent(text)"}}} {
			rows, err := fact.stmt.QueryContext(p.Ctx, fact.args...)
			if rows != nil {
				rows.Close()
				t.Error("role-invalidated input returned cursor")
			}
			regprocError(t, err, "42883", `function "unaccent(text)" does not exist`, fact.q, fact.route, "")
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("RESET ROLE", 0))
		recovery := "GRANT USAGE ON SCHEMA regproc_held TO regproc_held_reader"
		if !b.PG {
			recovery = "ALTER SCHEMA regproc_held OWNER TO regproc_held_reader"
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec(recovery, 0))
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("SET ROLE regproc_held_reader", 0))
		regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: held, SQL: literal}, r, fixture.Select(literal, []string{"id"}, []string{"INT8"}, [][]any{{replacement}}))
		for _, fact := range []struct {
			q    string
			stmt *sql.Stmt
		}{{frameQ, frame}, {runtimeQ, runtime}} {
			regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: fact.stmt, SQL: fact.q}, r, fixture.Select(fact.q, []string{"id"}, []string{"INT8"}, [][]any{{replacement}}, "unaccent(text)"))
			regprocStep(t, p, fixture.HeldStatement{Executor: c, Statement: fact.stmt, SQL: fact.q}, r, fixture.Select(fact.q, []string{"id"}, []string{"INT8"}, [][]any{{nil}}, nil))
		}
		fixture.RunStep(t, p.Ctx, c, r, fixture.Exec("RESET ROLE", 0))
		// Unknown numeric identities do not require an installed provider.
		regprocBoth(t, p, c, r, fixture.Select("SELECT $1::regprocedure AS p", []string{"p"}, []string{"REGPROCEDURE"}, [][]any{{"4294967295"}}, strconv.FormatUint(4294967295, 10)))
	})
}
