package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func intarraySeedSupport(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ia_support(id integer NOT NULL,k ia_provider.intbig_gkey,a ia_provider.intbig_gkey[])", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ia_support VALUES (1,NULL,'{}'),(2,NULL,'{NULL}'),(3,NULL,NULL),(4,NULL,'[0:1]={NULL,NULL}')", 4))
}

func intarraySupportCheck(b fixture.Backend) fixture.Step {
	return intarraySelect(b, "SELECT id,k,a FROM ia_support ORDER BY id", []string{"id", "k", "a"}, []string{"INT4", "INTBIG_GKEY", "_INTBIG_GKEY"}, []bool{false, true, true}, [][]any{{int64(1), nil, "{}"}, {int64(2), nil, "{NULL}"}, {int64(3), nil, nil}, {int64(4), nil, "[0:1]={NULL,NULL}"}})
}

func intarraySupportReads(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	intarrayBoth(t, p, b, c, intarraySupportCheck(b))
	for _, tc := range []struct {
		query  string
		values [][]any
	}{
		{"SELECT k,a FROM ia_support WHERE id=3", [][]any{{nil, nil}}},
		{"SELECT k,a FROM ia_support WHERE id=99", [][]any{}},
	} {
		intarrayBoth(t, p, b, c, intarraySelect(b, tc.query, []string{"k", "a"}, []string{"INTBIG_GKEY", "_INTBIG_GKEY"}, []bool{true, true}, tc.values))
	}
}

func TestReferenceIntarrayProviderSupportType(t *testing.T) {
	t.Run("values_and_accessors", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarrayInstall(t, p, b, c)
			intarraySeedSupport(t, p, b, c)
			intarraySupportReads(t, p, b, c)
			intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT NULL::ia_provider.intbig_gkey AS k", []string{"k"}, []string{"INTBIG_GKEY"}, nil, [][]any{{nil}}))
			intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT $1::ia_provider.intbig_gkey AS k", []string{"k"}, []string{"INTBIG_GKEY"}, nil, [][]any{{nil}}, nil))
			for _, q := range []string{"SELECT ia_provider._intbig_in(NULL) AS k", "SELECT ia_provider._intbig_out(NULL::ia_provider.intbig_gkey) AS k"} {
				family := "INTBIG_GKEY"
				if q == "SELECT ia_provider._intbig_out(NULL::ia_provider.intbig_gkey) AS k" {
					family = "CSTRING"
				}
				intarrayBoth(t, p, b, c, intarraySelect(b, q, []string{"k"}, []string{family}, nil, [][]any{{nil}}))
			}
			q := "SELECT $1::ia_provider.intbig_gkey[] AS a"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, value := range []any{"{}", "{NULL}", "[0:1]={NULL,NULL}", nil} {
				args := []any{value}
				if value != nil {
					args = append(args, []byte(value.(string)))
				}
				for _, arg := range args {
					s := intarraySelect(b, q, []string{"a"}, []string{"_INTBIG_GKEY"}, nil, [][]any{{value}}, arg)
					intarrayBoth(t, p, b, c, s)
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec("INSERT INTO ia_support VALUES (5,$1,$2)", 1, nil, "{NULL}")
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("DELETE FROM ia_support WHERE id=5", 1))
			}
			for _, q := range []string{
				"ALTER FUNCTION ia_provider._intbig_in(cstring) RENAME TO ia_key_input",
				"ALTER EXTENSION intarray DROP FUNCTION ia_provider.ia_key_input(cstring)",
				"ALTER FUNCTION ia_provider._intbig_out(ia_provider.intbig_gkey) RENAME TO ia_key_output",
				"ALTER EXTENSION intarray DROP FUNCTION ia_provider.ia_key_output(ia_provider.intbig_gkey)",
			} {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			}
			intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT ia_provider.ia_key_input(NULL) AS k", []string{"k"}, []string{"INTBIG_GKEY"}, nil, [][]any{{nil}}))
			intarrayBoth(t, p, b, c, intarraySelect(b, "SELECT ia_provider.ia_key_output(NULL::ia_provider.intbig_gkey) AS v", []string{"v"}, []string{"CSTRING"}, nil, [][]any{{nil}}))
			for _, prepared := range []bool{false, true} {
				intarrayError(t, p, b, c, "SELECT ia_provider.ia_key_input('x') AS k", prepared, "execute", intarrayReject())
			}
		})
	})
	t.Run("selected_input_gateway", func(t *testing.T) {
		intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			intarrayInstall(t, p, b, c)
			// Source-reasoned expectations for this new gateway: C-function
			// evaluation follows cstring conversion, rather than rejecting a
			// support-type cast at its literal token. Execute stage and every
			// absent diagnostic field require the first pinned-oracle proof;
			// these are not copied from the cast's Position 8 expectation.
			for _, prepared := range []bool{false, true} {
				route := "direct"
				if prepared {
					route = "prepared"
				}
				for _, tc := range []struct {
					name, query string
					args        []any
				}{
					{"literal", "SELECT ia_provider._intbig_in('x') AS k", nil},
					{"cstring_string", "SELECT ia_provider._intbig_in($1) AS k", []any{"x"}},
					{"cstring_bytes", "SELECT ia_provider._intbig_in($1) AS k", []any{[]byte("x")}},
				} {
					t.Run(route+"/"+tc.name, func(t *testing.T) {
						intarrayError(t, p, b, c, tc.query, prepared, "execute", intarrayReject(), tc.args...)
					})
				}
			}
		})
	})
	for _, prepared := range []bool{false, true} {
		route := "direct"
		if prepared {
			route = "prepared"
		}
		t.Run(route+"_rejection_diagnostics", func(t *testing.T) {
			intarrayScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				intarrayInstall(t, p, b, c)
				intarraySeedSupport(t, p, b, c)
				for _, q := range []string{"SELECT ''::ia_provider.intbig_gkey", "SELECT 'x'::ia_provider.intbig_gkey", "SELECT '{x}'::ia_provider.intbig_gkey[]"} {
					want := intarrayReject()
					want.Position = "8"
					stage := "execute"
					if prepared {
						stage = "prepare"
					}
					intarrayError(t, p, b, c, q, prepared, stage, want)
				}
				for _, tc := range []struct {
					query string
					arg   any
				}{
					{"SELECT $1::ia_provider.intbig_gkey", ""},
					{"SELECT $1::ia_provider.intbig_gkey", []byte("")},
					{"SELECT $1::ia_provider.intbig_gkey", "x"},
					{"SELECT $1::ia_provider.intbig_gkey", []byte("x")},
					{"SELECT $1::ia_provider.intbig_gkey[]", "{x}"},
					{"SELECT $1::ia_provider.intbig_gkey[]", []byte("{x}")},
					// Explicit text formal input exercises runtime conversion rather
					// than inferred support-type wire input. Keep both routes.
					{"SELECT ($1::text)::ia_provider.intbig_gkey", "x"},
				} {
					want := intarrayParameterReject(tc.arg)
					if strings.HasPrefix(tc.query, "SELECT ($1::text)") {
						want = intarrayReject()
					}
					intarrayError(t, p, b, c, tc.query, prepared, "execute", want, tc.arg)
				}
				q := "INSERT INTO ia_support VALUES (8,NULL,'{}'),(9,'x',NULL)"
				stage := "execute"
				if prepared {
					stage = "prepare"
				}
				want := intarrayReject()
				want.Position = "48"
				intarrayError(t, p, b, c, q, prepared, stage, want)
				intarraySupportReads(t, p, b, c)
			})
		})
	}
}
