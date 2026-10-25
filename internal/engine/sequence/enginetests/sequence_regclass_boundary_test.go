package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func sequenceBoundaryDiagnostic(t *testing.T, err error, want sqlstate.Error) {
	t.Helper()
	want.Severity, want.SeverityUnlocalized = "ERROR", "ERROR"
	var got *sqlstate.Error
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got=%#v err=%v want=%#v", got, err, want)
	}
}

// Hold the actual analysis description across executions, just as a prepared
// statement does; the direct route leaves Expected unset.
func sequenceBoundaryRunner(t *testing.T, e *core.Engine, op *session.Operation, query string, prepared bool) func(string) (core.Result, error) {
	t.Helper()
	ast := enginetest.Statement(t, query)
	description, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	return func(text string) (core.Result, error) {
		scope, err := e.StartStatement(op)
		if err != nil {
			return core.Result{}, err
		}
		if prepared {
			scope.Expected = &description
		}
		r, err := scope.Execute(ast, 1, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
			value, err := info.Types[0].Type.InputWithEnvironment(ctx, text, -1, input)
			if err == nil {
				frame[0] = value
			}
			if err != nil {
				return analyze.SequenceInputError(err, parser.Position{}, "", 1)
			}
			return nil
		})
		return r, scope.Finish(err)
	}
}

func TestSequenceRegclassCommentIdentityBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []string{"SELECT nextval($1)", "SELECT nextval($1) FROM empty_input", "SELECT setval($1,nextval('nested'))"} {
			for _, input := range []string{"base--suffix", "base/*suffix*/"} {
				t.Run(fmt.Sprintf("prepared=%t/%s/%s", prepared, query, input), func(t *testing.T) {
					e := engine.New()
					op, done := enginetest.RoleOperation(t, e, "postgres")
					defer done()
					for _, q := range []string{"CREATE SEQUENCE base START 17 CACHE 1", "CREATE SEQUENCE nested START 31 CACHE 1", "CREATE SEQUENCE remembered START 71 CACHE 1", "CREATE TABLE empty_input(id INT)"} {
						enginetest.RoleExecute(t, e, op, q)
					}
					r := enginetest.RoleExecute(t, e, op, "SELECT nextval('remembered')")
					r.Rows.Close()
					r, err := sequenceBoundaryRunner(t, e, op, query, prepared)(input)
					if r.Rows != nil {
						r.Rows.Close()
					}
					sequenceBoundaryDiagnostic(t, err, sqlstate.Error{Code: "42P01", Message: fmt.Sprintf(`relation "%s" does not exist`, input), Context: "unnamed portal parameter $1 = '...'"})
					for _, name := range []string{"base", "nested"} {
						_, err = e.Execute(op, enginetest.Statement(t, "SELECT currval('"+name+"')"), 0, nil)
						sequenceBoundaryDiagnostic(t, err, sqlstate.Error{Code: "55000", Message: fmt.Sprintf(`currval of sequence "%s" is not yet defined in this session`, name)})
					}
					for _, tc := range []struct {
						query string
						want  int64
					}{{"SELECT lastval()", 71}, {"SELECT nextval('base')", 17}, {"SELECT nextval('nested')", 31}} {
						r = enginetest.RoleExecute(t, e, op, tc.query)
						got := enginetest.Tuples(r)
						r.Rows.Close()
						if len(got) != 1 || got[0][0] != tc.want {
							t.Fatal(tc.query, got, tc.want)
						}
					}
				})
			}
		}
	}
}

func TestSequenceRegclassSymbolicIdentityBoundary(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
			e := engine.New()
			op, done := enginetest.RoleOperation(t, e, "postgres")
			defer done()
			long := strings.Repeat("界", 22)
			for _, q := range []string{`CREATE SEQUENCE "base--suffix" START 17`, `CREATE SEQUENCE "base/*suffix*/" START 31`, `CREATE SEQUENCE "s-other" START 41`, `CREATE SEQUENCE "123" START 51`, `CREATE SEQUENCE "-" START 61`, `CREATE SCHEMA "Mixed.Schema"`, `CREATE SEQUENCE "Mixed.Schema"."a""b.dot" START 71`, `CREATE SEQUENCE "` + long + `" START 81`, `CREATE SEQUENCE "s""quote" START 91`} {
				enginetest.RoleExecute(t, e, op, q)
			}
			run := sequenceBoundaryRunner(t, e, op, "SELECT nextval($1)", prepared)
			for _, tc := range []struct {
				input string
				want  int64
			}{{"BASE--suffix", 17}, {"base/*suffix*/", 31}, {"s-other", 41}, {" \t123\n", 51}, {" - ", 61}, {` "Mixed.Schema" . "a""b.dot" `, 71}, {long, 81}, {`s"quote`, 91}} {
				r, err := run(tc.input)
				if err != nil {
					t.Fatal(tc.input, err)
				}
				got := enginetest.Tuples(r)
				r.Rows.Close()
				if len(got) != 1 || got[0][0] != tc.want {
					t.Fatal(tc.input, got, tc.want)
				}
			}
			project := sequenceBoundaryRunner(t, e, op, "SELECT $1::regclass", prepared)
			for _, tc := range []struct{ input, canonical string }{{"123", "123"}, {"-", "0"}, {"4294967295", "4294967295"}} {
				r, err := project(tc.input)
				if err != nil {
					t.Fatal(tc.input, err)
				}
				frame := make([]sqltype.Value, 1)
				present, err := r.Rows.Next(context.Background(), frame)
				text, textErr := frame[0].Text()
				r.Rows.Close()
				if !present || err != nil || textErr != nil || text != tc.canonical {
					t.Fatal(tc.input, present, text, err, textErr)
				}
			}
		})
	}
}

func TestSequenceRegclassLiteralSyntaxBoundary(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	enginetest.RoleExecute(t, e, op, "CREATE SEQUENCE base START 17")
	for _, tc := range []struct{ input, code, message string }{{"base--suffix", "42P01", `relation "base--suffix" does not exist`}, {"base/*suffix*/", "42P01", `relation "base/*suffix*/" does not exist`}, {`""`, "42P01", `relation "" does not exist`}, {"base trailing", "42602", "invalid name syntax"}, {`"base"suffix`, "42602", "invalid name syntax"}, {"base.", "42602", "invalid name syntax"}, {".base", "42602", "invalid name syntax"}, {`"base`, "42602", "invalid name syntax"}, {"", "42602", "invalid name syntax"}, {" \t\n", "42602", "invalid name syntax"}, {"db.public.base", "0A000", `cross-database references are not implemented: "db.public.base"`}} {
		query := "SELECT nextval('" + tc.input + "')"
		_, err := e.Describe(op, enginetest.Statement(t, query))
		sequenceBoundaryDiagnostic(t, err, sqlstate.Error{Code: tc.code, Message: tc.message, Position: "16"})
	}
	r := enginetest.RoleExecute(t, e, op, "SELECT nextval('base')")
	got := enginetest.Tuples(r)
	r.Rows.Close()
	if got[0][0] != int64(17) {
		t.Fatal("failed literal analysis consumed prefix", got)
	}
}
