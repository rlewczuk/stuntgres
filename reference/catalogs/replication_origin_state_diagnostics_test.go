package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func originStateError(query, code, message, detail string, args ...any) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: code, Message: message, Detail: detail}, args...)
}
func originStateDuplicate(query, name string, args ...any) fixture.Step {
	return fixture.ImplicitError(query, fixture.Diagnostic{Code: "23505", Message: `duplicate key value violates unique constraint "pg_replication_origin_roname_index"`, Detail: "Key (roname)=(" + name + ") already exists.", Schema: "pg_catalog", Table: "pg_replication_origin", Constraint: "pg_replication_origin_roname_index"}, args...)
}
func TestReferenceReplicationOriginStateNames(t *testing.T) {
	samples := []string{"", "MiXeD name", "  whitespace  ", "quote'newline\nslash\\", "PG_allowed", strings.Repeat("x", 512), strings.Repeat("é", 256)}
	for i, name := range samples {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					originStateCleanup(t, b.DB, name)
					for _, prepared := range []bool{false, true} {
						q := "SELECT pg_replication_origin_create($1::text) AS v"
						// The ID is generated, so compare its relation to the live catalog rather
						// than fixing PostgreSQL's allocator sequence.
						if prepared {
							stmt, err := b.DB.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							var id int64
							err = stmt.QueryRowContext(p.Ctx, name).Scan(&id)
							stmt.Close()
							if err != nil || id != originStateID(t, p, b.DB, name) {
								t.Fatal(id, err)
							}
						} else {
							originStateCreate(t, p, b, b.DB, name)
						}
						originStateProgress(t, p, b, b.DB, name, nil)
						duplicate := originStateDuplicate(q, name, name)
						duplicate.Prepared = prepared
						originStateRun(t, p, b, b.DB, duplicate)
						originStateRun(t, p, b, b.DB, originStateScalar("SELECT pg_replication_origin_drop($1::text) AS v", "VOID", "", name))
					}
				})
			}
		})
	}
	for _, name := range []string{strings.Repeat("x", 513), strings.Repeat("é", 256) + "a", "any", "ANY", "nOnE", "pg_reserved", "pg_" + strings.Repeat("x", 513)} {
		t.Run(fmt.Sprintf("invalid_%d_%s", len(name), name[:min(3, len(name))]), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				code, message, detail := "54000", "replication origin name is too long", "Replication origin names must be no longer than 512 bytes."
				if strings.EqualFold(name, "any") || strings.EqualFold(name, "none") || strings.HasPrefix(name, "pg_") {
					code = "42939"
					message = `replication origin name "` + name + `" is reserved`
					detail = `Origin names "any", "none", and names starting with "pg_" are reserved.`
				}
				for _, prepared := range []bool{false, true} {
					s := originStateError("SELECT pg_replication_origin_create($1::text)", code, message, detail, name)
					s.Prepared = prepared
					originStateRun(t, p, b, b.DB, s)
				}
				originStateRun(t, p, b, b.DB, originStateScalar("SELECT pg_replication_origin_oid($1::text) AS v", "OID", nil, name))
			}
		})
	}
}
func TestReferenceReplicationOriginStateDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			name := "state_\"quoted'\n"
			originStateCleanup(t, b.DB, name)
			id := originStateCreate(t, p, b, b.DB, name)
			originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance($1::text,'80000000/0')", 1, name))
			for _, prepared := range []bool{false, true} {
				s := originStateDuplicate("SELECT pg_replication_origin_create($1::text)", name, name)
				s.Prepared = prepared
				originStateRun(t, p, b, b.DB, s)
				if originStateID(t, p, b.DB, name) != id {
					t.Fatal("duplicate changed identity")
				}
				originStateProgress(t, p, b, b.DB, name, "80000000/0")
				for _, q := range []string{"SELECT pg_replication_origin_drop($1::text)", "SELECT pg_replication_origin_advance($1::text,'0/1')", "SELECT pg_replication_origin_progress($1::text,false)", "SELECT pg_replication_origin_progress($1::text,true)"} {
					s := originStateError(q, "42704", `replication origin "missing_"quoted'`+"\n"+`" does not exist`, "", "missing_\"quoted'\n")
					s.Prepared = prepared
					originStateRun(t, p, b, b.DB, s)
				}
			}
		})
	}
}
func TestReferenceReplicationOriginStateBinding(t *testing.T) {
	const hint = "No function matches the given name and argument types. You might need to add explicit type casts."
	type sample struct {
		q, signature string
		position     int
	}
	var cases []sample
	for i, fn := range originStateFunctions {
		for _, prefix := range []string{"SELECT ", "SELECT * FROM ", "/*é*/ SELECT "} {
			position := len([]rune(prefix)) + 1
			cases = append(cases, sample{prefix + fn + "()", fn + "()", position})
			cases = append(cases, sample{prefix + fn + "(1)", fn + "(integer)", position})
			if i < 3 {
				cases = append(cases, sample{prefix + fn + "('x','y')", fn + "(unknown, unknown)", position})
			} else {
				cases = append(cases, sample{prefix + fn + "('x')", fn + "(unknown)", position}, sample{prefix + fn + "('x',1)", fn + "(unknown, integer)", position}, sample{prefix + fn + "('x',NULL,NULL)", fn + "(unknown, unknown, unknown)", position})
			}
			args := "node_name => 'x'"
			signature := "node_name => unknown"
			if i == 3 {
				args += ",lsn => '0/1'"
				signature += ", lsn => unknown"
			}
			if i == 4 {
				args += ",flush => false"
				signature += ", flush => boolean"
			}
			cases = append(cases, sample{prefix + fn + "(" + args + ")", fn + "(" + signature + ")", position})
		}
	}
	cases = append(cases, sample{"SELECT pg_replication_origin_absent('x')", "pg_replication_origin_absent(unknown)", 8}, sample{"SELECT public.pg_replication_origin_oid('x')", "public.pg_replication_origin_oid(unknown)", 8}, sample{"SELECT pg_replication_origin_advance('x','0/1'::text)", "pg_replication_origin_advance(unknown, text)", 8}, sample{"SELECT pg_replication_origin_progress('x','false'::text)", "pg_replication_origin_progress(unknown, text)", 8})
	for i, tc := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42883", Message: "function " + tc.signature + " does not exist", Hint: hint, Position: fixture.Position(tc.position)})
				originStateRun(t, p, b, b.DB, s)
				originStateRun(t, p, b, b.DB, fixture.Prepared(s))
			}
		})
	}
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		originStateRun(t, p, b, b.DB, fixture.ImplicitError("SELECT absent_schema.pg_replication_origin_oid('x')", fixture.Diagnostic{Code: "3F000", Message: `schema "absent_schema" does not exist`, Position: fixture.Position(8)}))
		q := "SELECT pg_replication_origin_advance('x','bad'::pg_lsn)"
		s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type pg_lsn: "bad"`, Position: fixture.Position(42)})
		originStateRun(t, p, b, b.DB, s)
		originStateRun(t, p, b, b.DB, fixture.Prepared(s))
		parameter := fixture.ImplicitError("SELECT pg_replication_origin_advance($1::text,$2::pg_lsn)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type pg_lsn: "bad"`, Context: "unnamed portal parameter $2 = '...'"}, "missing", "bad")
		parameter.ErrorStage = "execute"
		parameter.SelectRows = true
		originStateRun(t, p, b, b.DB, parameter)
		originStateRun(t, p, b, b.DB, fixture.Prepared(parameter))
	}
}
func TestReferenceReplicationOriginStateNulls(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			originStateCleanup(t, b.DB, "state_nulls")
			id := originStateCreate(t, p, b, b.DB, "state_nulls")
			originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance('state_nulls','0/7')", 1))
			cases := []struct{ call, typ string }{
				{"pg_replication_origin_create(NULL)", "OID"}, {"pg_replication_origin_drop(NULL)", "VOID"}, {"pg_replication_origin_oid(NULL)", "OID"},
				{"pg_replication_origin_advance(NULL,'0/1')", "VOID"}, {"pg_replication_origin_advance('missing',NULL::pg_lsn)", "VOID"},
				{"pg_replication_origin_progress(NULL,true)", "PG_LSN"}, {"pg_replication_origin_progress('state_nulls',NULL::boolean)", "PG_LSN"},
			}
			for _, tc := range cases {
				s := originStateScalar("SELECT "+tc.call+" AS v", tc.typ, nil)
				originStateRun(t, p, b, b.DB, s)
				originStateRun(t, p, b, b.DB, fixture.Prepared(s))
			}
			if originStateID(t, p, b.DB, "state_nulls") != id {
				t.Fatal("strict null changed identity")
			}
			originStateProgress(t, p, b, b.DB, "state_nulls", "0/7")
			originStateRun(t, p, b, b.DB, fixture.Exec("CREATE SEQUENCE state_null_steps", 0))
			erased := originStateScalar("SELECT pg_replication_origin_progress(NULL,nextval('state_null_steps')::integer::boolean) AS v", "PG_LSN", nil)
			originStateRun(t, p, b, b.DB, erased)
			originStateRun(t, p, b, b.DB, fixture.Prepared(erased))
			originStateRun(t, p, b, b.DB, originStateScalar("SELECT is_called AS v FROM state_null_steps", "BOOL", false))
			originStateRun(t, p, b, b.DB, fixture.ImplicitError("SELECT pg_replication_origin_advance(NULL,'bad'::pg_lsn)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type pg_lsn: "bad"`, Position: fixture.Position(43)}))
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
				originStateRun(t, p, b, c, fixture.Exec("SET plan_cache_mode="+mode, 0))
				q := "SELECT pg_replication_origin_advance($1::text,$2::pg_lsn) AS v"
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				originStateRun(t, p, b, held, originStateScalar(q, "VOID", nil, "missing", nil))
				stmt.Close()
			}
			originStateRun(t, p, b, c, fixture.Exec("RESET plan_cache_mode", 0))
		})
	}
}
func TestReferenceReplicationOriginStateDurability(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			name := "state_durability"
			originStateCleanup(t, b.DB, name)
			originStateCreate(t, p, b, b.DB, name)
			q := "SELECT pg_replication_origin_progress($1::text,$2::boolean) AS v"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			for _, position := range []string{"", "80000000/1", "0/0"} {
				if position != "" {
					originStateRun(t, p, b, b.DB, fixture.Exec("SELECT pg_replication_origin_advance($1::text,$2::pg_lsn)", 1, name, position))
				}
				var want any = position
				if position == "" || position == "0/0" {
					want = nil
				}
				for _, e := range []fixture.Executor{b.DB, held} {
					s := originStateScalar(q, "PG_LSN", want, name, true)
					if !b.PG {
						s = originStateError(q, "0A000", originStateFlushBoundary, "", name, true)
					}
					originStateRun(t, p, b, e, s)
					originStateRun(t, p, b, e, originStateScalar(q, "PG_LSN", want, name, false))
					originStateRun(t, p, b, e, originStateScalar(q, "PG_LSN", nil, nil, true))
					originStateRun(t, p, b, e, originStateError(q, "42704", `replication origin "state_missing" does not exist`, "", "state_missing", true))
				}
			}
		})
	}
}
