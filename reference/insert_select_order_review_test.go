package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceInsertSelectOrderNumericClassification(t *testing.T) {
	for _, key := range []string{"+2", "+2 ASC", "+2 DESC", "+1.5", "+1.5 DESC", "+1e0", "+2147483648", "1", "+0", "-0", "0", "2147483647", "2147483648", "2147483649", "-2147483647", "-2147483648", "-2147483649", "1e0", "+1e131072"} {
		for _, prepared := range []bool{false, true} {
			mode := "direct"
			if prepared {
				mode = "prepare"
			}
			t.Run(key+"/"+mode, func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						run := func(s referenceStep) { insertSelectRun(t, p.ctx, backend.db, backend.recorder, s) }
						run(referenceExec("CREATE TABLE t(i INT)", 0))
						run(referenceExec("INSERT INTO t VALUES(9)", 1))
						q := "INSERT INTO t VALUES(1),(2) ORDER BY " + key
						if key == "-2147483649" || key == "+1e131072" {
							q = "/*é*/ " + q
						}
						if (strings.HasPrefix(key, "+") && key != "+1e131072") || key == "1" {
							if prepared {
								stmt, err := backend.db.PrepareContext(p.ctx, q)
								if err != nil {
									t.Fatal(err)
								}
								defer stmt.Close()
								run(referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
								held := referenceHeldStatement{backend.db, stmt, q}
								insertSelectRun(t, p.ctx, held, backend.recorder, referenceExec(q, 2))
							} else {
								run(referenceExec(q, 2))
							}
							run(referenceSelect("SELECT i FROM t ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(9)}}))
							return
						}
						position := utf8.RuneCountInString(q[:strings.Index(q, "ORDER BY ")+len("ORDER BY ")]) + 1
						want := referenceDiagnostic{Code: "42601", Message: "non-integer constant in ORDER BY", Position: strconv.Itoa(position)}
						if key == "+1e131072" {
							want.Code, want.Message, want.Position = "22003", "value overflows numeric format", strconv.Itoa(position+1)
						}
						if key == "0" || key == "-0" || key == "2147483647" || key == "-2147483647" {
							n, _ := strconv.ParseInt(key, 10, 32)
							want.Code = "42P10"
							want.Message = "ORDER BY position " + strconv.FormatInt(n, 10) + " is not in select list"
						}
						t.Run("diagnostic", func(t *testing.T) {
							s := referenceImplicitError(q, want)
							s.prepared = prepared
							s.errorStage = "execute"
							if prepared {
								s.errorStage = "prepare"
							}
							run(s)
						})
						run(referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
					})
				}
			})
		}
	}
}

func TestReferenceInsertSelectOrderDiagnosticPrecedence(t *testing.T) {
	for _, key := range []string{"'x'", "TRUE", "NULL", "1.5"} {
		for _, tc := range []struct{ name, q, code, message, token string }{
			{"relation", "INSERT INTO missing VALUES(1) ORDER BY " + key, "42P01", `relation "missing" does not exist`, "missing"},
			{"column", "INSERT INTO t(missing) VALUES(1) ORDER BY " + key, "42703", `column "missing" of relation "t" does not exist`, "missing"},
			{"common_type", "INSERT INTO t VALUES(1),(TRUE) ORDER BY " + key, "42804", "VALUES types integer and boolean cannot be matched", "TRUE"},
			{"later_script", "INSERT INTO t VALUES(8); INSERT INTO t VALUES(1) ORDER BY " + key + "; CREATE TABLE select(x INT)", "42601", `syntax error at or near "select"`, "select"},
		} {
			for _, prepared := range []bool{false, true} {
				mode := "direct"
				if prepared {
					mode = "prepare"
				}
				t.Run(key+"/"+tc.name+"/"+mode, func(t *testing.T) {
					p := newReferenceScenario(t)
					for _, backend := range p.backends() {
						t.Run(backend.name, func(t *testing.T) {
							run := func(s referenceStep) { insertSelectRun(t, p.ctx, backend.db, backend.recorder, s) }
							run(referenceExec("CREATE TABLE t(i INT)", 0))
							run(referenceExec("INSERT INTO t VALUES(9)", 1))
							t.Run("diagnostic", func(t *testing.T) {
								s := referenceImplicitError(tc.q, referenceDiagnostic{Code: tc.code, Message: tc.message, Position: strconv.Itoa(strings.Index(tc.q, tc.token) + 1)})
								s.prepared = prepared
								s.errorStage = "execute"
								if prepared {
									s.errorStage = "prepare"
								}
								run(s)
							})
							run(referenceSelect("SELECT i FROM t", []string{"i"}, []string{"INT4"}, [][]any{{int64(9)}}))
						})
					}
				})
			}
		}
	}
}
