package reference_test

import (
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// NIST SP 800-38A, F.1.1/F.1.2 and F.2.1/F.2.2, first AES-128 block.
// Decryption consumes published ciphertext, never output learned from native.
func TestReferenceObjectGrantPGCryptoCipherAuthority(t *testing.T) {
	const plain = "6bc1bee22e409f96e93d7e117393172a"
	const key = "2b7e151628aed2a6abf7158809cf4f3c"
	const iv = "000102030405060708090a0b0c0d0e0f"
	const ecb = "3ad77bb40d7a3660a89ecaf32466ef97"
	const cbc = "7649abac8119b246cee98e9b12e9197d"
	bytes := func(h string) []byte {
		v, err := hex.DecodeString(h)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	plaintext := string(bytes(plain))
	members := []struct {
		name, signature, input, output, scheme, symbol string
		columns                                        []string
	}{
		{"encrypt", "bytea,bytea,text", plain, ecb, "aes-ecb/pad:none", "pg_encrypt", []string{"d", "k", "typ"}},
		{"decrypt", "bytea,bytea,text", ecb, plain, "aes-ecb/pad:none", "pg_decrypt", []string{"d", "k", "typ"}},
		{"encrypt_iv", "bytea,bytea,bytea,text", plain, cbc, "aes-cbc/pad:none", "pg_encrypt_iv", []string{"d", "k", "iv", "typ"}},
		{"decrypt_iv", "bytea,bytea,bytea,text", cbc, plain, "aes-cbc/pad:none", "pg_decrypt_iv", []string{"d", "k", "iv", "typ"}},
	}
	for _, tc := range members {
		t.Run(tc.name, func(t *testing.T) {
			objectGrantScenario(t, []string{"og_cipher"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				if b.PG {
					clean := func() {
						for _, q := range []string{"ROLLBACK", "RESET SESSION AUTHORIZATION", "DROP EXTENSION IF EXISTS pgcrypto CASCADE"} {
							if _, err := c.ExecContext(p.Ctx, q); err != nil {
								t.Error(q, err)
							}
						}
					}
					defer clean()
					clean()
				}
				run(fixture.Exec("CREATE SCHEMA og_cipher", 0))
				run(fixture.Exec("CREATE EXTENSION pgcrypto SCHEMA og_cipher", 0))
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_cipher_source(id INT,d BYTEA,k BYTEA,iv BYTEA,typ TEXT,p BYTEA,z INT)", 0))
				insert := "INSERT INTO og_cipher_source VALUES($1,$2,$3,$4,$5,$6,0)"
				for _, id := range []int64{1, 5} {
					run(fixture.Exec(insert, 1, id, bytes(tc.input), bytes(key), bytes(iv), tc.scheme, bytes(plain)))
				}
				run(fixture.Exec(insert, 1, int64(2), nil, nil, nil, nil, nil))
				run(fixture.Exec(insert, 1, int64(3), bytes(tc.input), bytes(key), bytes(iv), "unknown/pad", bytes(plain)))
				run(fixture.Exec(insert, 1, int64(4), []byte{1}, bytes(key), bytes(iv), tc.scheme, bytes(plain)))
				run(fixture.Exec("GRANT SELECT ON og_cipher_source TO og_object_reader", 0))
				call := "og_cipher." + tc.name + "(" + strings.Join(tc.columns, ",") + ")"
				expected := string(bytes(tc.output))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RoleGrantPermissionDenied(run, "SELECT "+call+" FROM og_cipher_source WHERE id=1", "schema", "og_cipher", prepared)
				})
				run(fixture.Exec("GRANT USAGE ON SCHEMA og_cipher TO og_object_reader", 0))

				// Dynamic identities and membership of the selected four, not a stale
				// total-provider count (the current provider has fourteen members).
				seen := make(map[int64]bool)
				for _, member := range members {
					var oid int64
					identity := "og_cipher." + member.name + "(" + member.signature + ")"
					if err := c.QueryRowContext(p.Ctx, "SELECT $1::regprocedure::oid", identity).Scan(&oid); err != nil || oid <= 0 || seen[oid] {
						t.Fatal("installed cipher identity", identity, oid, err)
					}
					seen[oid] = true
					full := fixture.Select("SELECT prorettype::BIGINT AS result,prokind,prolang::BIGINT AS language,proisstrict,provolatile,proparallel,proretset,pronargdefaults,provariadic::BIGINT AS variadic,proargnames IS NULL AS unnamed,prosrc FROM pg_proc WHERE oid=$1", []string{"result", "prokind", "language", "proisstrict", "provolatile", "proparallel", "proretset", "pronargdefaults", "variadic", "unnamed", "prosrc"}, []string{"INT8", "CHAR", "INT8", "BOOL", "CHAR", "CHAR", "BOOL", "INT2", "INT8", "BOOL", "TEXT"}, [][]any{{int64(17), "f", int64(13), true, "i", "s", false, int64(0), int64(0), true, member.symbol}}, oid)
					if b.PG {
						run(full)
					}
					run(fixture.Select("SELECT p.oid::regprocedure::TEXT AS identity,p.pronargs,p.prorettype::BIGINT AS result,p.provolatile,p.proisstrict,p.proparallel,n.nspname FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.oid=$1", []string{"identity", "pronargs", "result", "provolatile", "proisstrict", "proparallel", "nspname"}, []string{"TEXT", "INT2", "INT8", "CHAR", "BOOL", "CHAR", "NAME"}, [][]any{{identity, int64(len(member.columns)), int64(17), "i", true, "s", "og_cipher"}}, oid))
					run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid JOIN pg_namespace n ON n.oid=e.extnamespace WHERE d.classid=1255 AND d.objid=$1 AND d.objsubid=0 AND d.refclassid=3079 AND d.refobjsubid=0 AND d.deptype='e' AND e.extname='pgcrypto' AND e.extversion='1.4' AND e.extrelocatable AND n.nspname='og_cipher'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}, oid))
				}
				value := func(expr, tail string, v any, args ...any) fixture.Step {
					return fixture.Select("SELECT "+expr+" AS v FROM og_cipher_source"+tail, []string{"v"}, []string{"BYTEA"}, [][]any{{v}}, args...)
				}
				failure := func(q, code, message string, args ...any) fixture.Step {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message}, args...)
					s.SelectRows, s.ErrorStage = true, "execute"
					return s
				}
				args := []any{bytes(tc.input), bytes(key), tc.scheme}
				literal := []string{"'\\x" + tc.input + "'::BYTEA", "'\\x" + key + "'::BYTEA", "'" + tc.scheme + "'::TEXT"}
				if len(tc.columns) == 4 {
					args = []any{args[0], args[1], bytes(iv), tc.scheme}
					literal = []string{literal[0], literal[1], "'\\x" + iv + "'::BYTEA", literal[2]}
				}
				parameters, rowParameters := make([]string, len(args)), make([]string, len(args))
				for i, col := range tc.columns {
					typ := "BYTEA"
					if col == "typ" {
						typ = "TEXT"
					}
					parameters[i] = fmt.Sprintf("$%d::%s", i+1, typ)
					rowParameters[i] = "CASE WHEN id=1 THEN " + parameters[i] + " ELSE " + col + " END"
				}
				parameterCall := "og_cipher." + tc.name + "(" + strings.Join(rowParameters, ",") + ")"
				run(fixture.Exec("CREATE VIEW og_cipher_view AS SELECT "+call+" AS v FROM og_cipher_source WHERE id=1", 0))
				run(fixture.Exec("GRANT SELECT ON og_cipher_view TO og_object_reader", 0))
				kernelMessage := tc.name + " error: Encryption failed"
				if strings.HasPrefix(tc.name, "decrypt") {
					kernelMessage = tc.name + " error: Decryption failed"
				}
				cases := []objectGrantProviderCase{
					{value(call, " WHERE id=1", expected), true, true},
					{value(parameterCall, " WHERE id=1", expected, args...), true, true},
					{fixture.Select("SELECT v FROM og_cipher_view", []string{"v"}, []string{"BYTEA"}, [][]any{{expected}}), true, true},
					{value(call, " WHERE id=2", nil), true, true},
					{failure("SELECT "+call+" AS v FROM og_cipher_source WHERE id=3", "22023", `Cannot use "unknown/pad": Badly formatted type`), true, true},
					{failure("SELECT "+call+" AS v FROM og_cipher_source WHERE id=4", "39000", kernelMessage), true, true},
					{value("CASE WHEN FALSE THEN "+call+" ELSE NULL::BYTEA END", " WHERE id=1", nil), false, true},
				}
				for i := range args {
					// Each formal has a runtime NULL witness, a literal planning NULL,
					// and a fresh imported NULL. No generic held bind specialization.
					data, k, vector, scheme := any(bytes(tc.input)), any(bytes(key)), any(bytes(iv)), any(tc.scheme)
					switch tc.columns[i] {
					case "d":
						data = nil
					case "k":
						k = nil
					case "iv":
						vector = nil
					case "typ":
						scheme = nil
					}
					run(fixture.Exec(insert, 1, int64(10+i), data, k, vector, scheme, bytes(plain)))
					cases = append(cases, objectGrantProviderCase{value(call, fmt.Sprintf(" WHERE id=%d", 10+i), nil), true, true})
					nullLiteral := append([]string(nil), literal...)
					nullLiteral[len(nullLiteral)-1] = "'unknown/pad'::TEXT"
					nullLiteral[i] = "NULL::BYTEA"
					if i == len(args)-1 {
						nullLiteral[i] = "NULL::TEXT"
					}
					cases = append(cases, objectGrantProviderCase{value("og_cipher."+tc.name+"("+strings.Join(nullLiteral, ",")+")", " WHERE id=1", nil), false, true})
					nullArgs := append([]any(nil), args...)
					nullArgs[len(nullArgs)-1] = "unknown/pad"
					nullArgs[i] = nil
					cases = append(cases, objectGrantProviderCase{value("og_cipher."+tc.name+"("+strings.Join(parameters, ",")+")", " WHERE id=1", nil, nullArgs...), false, false})
				}
				childArgs := append([]string(nil), tc.columns...)
				childArgs[len(childArgs)-1] = "(1/z)::TEXT"
				cases = append(cases, objectGrantProviderCase{failure("SELECT og_cipher."+tc.name+"("+strings.Join(childArgs, ",")+") AS v FROM og_cipher_source WHERE id=2", "22012", "division by zero"), true, true})
				if len(args) == 4 {
					// ECB does not use the IV bytes, but the IV is still a strict
					// SQL formal. A known NULL prunes; a runtime NULL still needs X.
					ecbNull := append([]string(nil), literal...)
					ecbNull[2], ecbNull[3] = "NULL::BYTEA", "'aes-ecb/pad:none'"
					cases = append(cases, objectGrantProviderCase{value("og_cipher."+tc.name+"("+strings.Join(ecbNull, ",")+")", " WHERE id=1", nil), false, true})
				}
				// Both real SQL calls survive on row inputs. Each of the four outer
				// scenarios revokes exactly one member, never both simultaneously.
				inner, outer := "encrypt", "decrypt"
				innerArgs, outerArgs := "p,k,typ", ",k,typ"
				if len(args) == 4 {
					inner, outer = "encrypt_iv", "decrypt_iv"
					innerArgs, outerArgs = "p,k,iv,typ", ",k,iv,typ"
				}
				nested := "og_cipher." + outer + "(og_cipher." + inner + "(" + innerArgs + ")" + outerArgs + ")"
				cases = append(cases, objectGrantProviderCase{value(nested, " WHERE id=1", plaintext), true, true})
				controls := []fixture.Step{}
				for _, sibling := range members {
					if sibling.name == tc.name {
						continue
					}
					operands := "'\\x" + sibling.input + "'::BYTEA,k,"
					if len(sibling.columns) == 4 {
						operands += "iv,"
					}
					controls = append(controls, value("og_cipher."+sibling.name+"("+operands+"'"+sibling.scheme+"')", " WHERE id=1", string(bytes(sibling.output))))
				}
				target := "og_cipher." + tc.name + "(" + tc.signature + ")"
				objectGrantProviderLifecycle(t, p, b, c, run, target, "function", tc.name, cases, controls)

				// Immutable stored generation and BYTEA btree expression index use
				// existing native capabilities. Old stored values need no new call.
				run(fixture.Exec("CREATE TABLE og_cipher_written(id INT,d BYTEA,k BYTEA,iv BYTEA,typ TEXT,g BYTEA GENERATED ALWAYS AS ("+call+") STORED)", 0))
				run(fixture.Exec("CREATE INDEX og_cipher_written_idx ON og_cipher_written (("+call+"))", 0))
				run(fixture.Exec("INSERT INTO og_cipher_written(id,d,k,iv,typ) SELECT 0,d,k,iv,typ FROM og_cipher_source WHERE id=1", 1))
				run(fixture.Exec("GRANT INSERT,SELECT ON og_cipher_written TO og_object_reader", 0))
				write := "INSERT INTO og_cipher_written(id,d,k,iv,typ) SELECT id,d,k,iv,typ FROM og_cipher_source WHERE id=1 OR id=5 ORDER BY id"
				stored := fixture.Select("SELECT id,g FROM og_cipher_written ORDER BY id", []string{"id", "g"}, []string{"INT4", "BYTEA"}, [][]any{{int64(0), expected}})
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM PUBLIC", 0))
				deniedWrite := func() {
					fixture.RoleGrantActor(run, "og_object_reader", func() {
						s := fixture.ImplicitError(write, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + tc.name})
						s.ErrorStage = "execute"
						run(s)
						run(stored)
					})
				}
				deniedWrite()
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(fixture.Exec(write, 2)) })
				stored.Rows = [][]any{{int64(0), expected}, {int64(1), expected}, {int64(5), expected}}
				run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+target+" FROM og_object_reader", 0))
				deniedWrite()

				// Import/analysis precedes selected admission; body errors above do
				// not. These full diagnostics are PG-first draft expectations.
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					// A database/sql string bound as BYTEA is byte data in pq,
					// not a malformed SQL BYTEA literal.
					badArgs := append([]any(nil), args...)
					badArgs[0] = `\xzz`
					q := "SELECT og_cipher." + tc.name + "(" + strings.Join(parameters, ",") + ") AS v"
					run(failure(q, "42501", "permission denied for function "+tc.name, badArgs...))
					badArgs[1] = nil
					run(fixture.Select(q, []string{"v"}, []string{"BYTEA"}, [][]any{{nil}}, badArgs...))
					// A genuinely malformed integer import precedes both selected
					// EXECUTE admission and strict NULL sibling elimination.
					importParameters := append([]string(nil), parameters...)
					last := len(args) - 1
					importParameters[last] = fmt.Sprintf("($%d::INTEGER)::TEXT", last+1)
					importArgs := append([]any(nil), args...)
					importArgs[last] = "oops"
					for _, null := range []bool{false, true} {
						if null {
							importArgs[1] = nil
						}
						importQuery := "SELECT og_cipher." + tc.name + "(" + strings.Join(importParameters, ",") + ") AS v"
						s := failure(importQuery, "22P02", `invalid input syntax for type integer: "oops"`, importArgs...)
						s.Diagnostic.Context = fmt.Sprintf("unnamed portal parameter $%d = '...'", last+1)
						run(s)
					}
					badLiteral := append([]string(nil), literal...)
					badLiteral[0] = "'\\xzz'::BYTEA"
					q = "SELECT og_cipher." + tc.name + "(" + strings.Join(badLiteral, ",") + ") AS v"
					s := failure(q, "22023", `invalid hexadecimal digit: "z"`)
					s.Diagnostic.Position = fixture.Position(strings.Index(q, "'\\xzz'") + 1)
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
					badLiteral[1] = "NULL::BYTEA"
					q = "SELECT og_cipher." + tc.name + "(" + strings.Join(badLiteral, ",") + ") AS v"
					s = failure(q, "22023", `invalid hexadecimal digit: "z"`)
					s.Diagnostic.Position = fixture.Position(strings.Index(q, "'\\xzz'") + 1)
					if prepared {
						s.ErrorStage = "prepare"
					}
					run(s)
				})
				run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+target+" TO og_object_reader", 0))
				run(fixture.Exec("REVOKE USAGE ON SCHEMA og_cipher FROM og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() {
					fixture.RoleGrantPermissionDenied(run, "SELECT "+call+" FROM og_cipher_source WHERE id=1", "schema", "og_cipher", prepared)
				})
				run(fixture.Exec("GRANT USAGE ON SCHEMA og_cipher TO og_object_reader", 0))
				fixture.RoleGrantActor(run, "og_object_reader", func() { run(value(call, " WHERE id=1", expected)) })
			})
		})
	}
}
