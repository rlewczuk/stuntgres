package reference_test

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceInsertImplicitShortDiagnostics(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE t(id INT NOT NULL,big BIGINT,label TEXT,flag BOOL)", 0))
	p.step(t, referenceExec("INSERT INTO t VALUES (99)", 1))
	check := func(preparedStage, query, code, message string, position int, detail, hint, schema, table, column string) {
		t.Helper()
		step := referenceImplicitError(query, referenceDiagnostic{Code: code, Message: message, Position: referencePosition(position), Detail: detail, Hint: hint, Schema: schema, Table: table, Column: column})
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), nil, nil, nil}}))
		step.errorStage = preparedStage
		p.step(t, referencePrepared(step))
		p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), nil, nil, nil}}))
	}
	q := "INSERT INTO t VALUES (20),('oops')"
	check("prepare", q, "22P02", `invalid input syntax for type integer: "oops"`, strings.Index(q, "'oops'")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20),(21,22)"
	check("prepare", q, "42601", "VALUES lists must all be the same length", strings.Index(q, "21,22")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20,30),(21)"
	check("prepare", q, "42601", "VALUES lists must all be the same length", strings.Index(q, "21)")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES ('2147483648')"
	check("prepare", q, "22003", `value "2147483648" is out of range for type integer`, strings.Index(q, "'2147483648'")+1, "", "", "", "", "")
	for _, query := range []string{
		"INSERT INTO t VALUES (2147483648)",
		"INSERT INTO t VALUES (20),(-2147483649)",
		"INSERT INTO t VALUES (NULL),(2147483648)",
	} {
		step := referenceImplicitError(query, referenceDiagnostic{Code: "22003", Message: "integer out of range"})
		step.errorStage = "execute"
		p.step(t, step)
		p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), nil, nil, nil}}))
		p.step(t, referencePrepared(step))
		p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), nil, nil, nil}}))
	}
	q = "INSERT INTO t VALUES (20,'oops')"
	check("prepare", q, "22P02", `invalid input syntax for type bigint: "oops"`, strings.Index(q, "'oops'")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (20,30,NULL,'maybe')"
	check("prepare", q, "22P02", `invalid input syntax for type boolean: "maybe"`, strings.Index(q, "'maybe'")+1, "", "", "", "", "")
	q = "INSERT INTO t VALUES (TRUE)"
	check("prepare", q, "42804", `column "id" is of type integer but expression is of type boolean`, strings.Index(q, "TRUE")+1, "", "You will need to rewrite or cast the expression.", "", "", "")
	q = "INSERT INTO t VALUES (20),(TRUE)"
	check("prepare", q, "42804", `column "id" is of type integer but expression is of type boolean`, strings.Index(q, "TRUE")+1, "", "You will need to rewrite or cast the expression.", "", "", "")
	q = "INSERT INTO t VALUES (20,30,40,TRUE,50)"
	check("prepare", q, "42601", "INSERT has more expressions than target columns", strings.Index(q, "50)")+1, "", "", "", "", "")
	q = "INSERT INTO t (id,label) VALUES (20)"
	check("prepare", q, "42601", "INSERT has more target columns than expressions", strings.Index(q, "label")+1, "", "", "", "", "")
	q = "INSERT INTO t (id,label) VALUES ('oops')"
	check("prepare", q, "42601", "INSERT has more target columns than expressions", strings.Index(q, "label")+1, "", "", "", "", "")
	q = "INSERT INTO t (id,id) VALUES (20,21)"
	check("prepare", q, "42701", `column "id" specified more than once`, strings.LastIndex(q, "id)")+1, "", "", "", "", "")
	q = "INSERT INTO t (missing) VALUES (20)"
	check("prepare", q, "42703", `column "missing" of relation "t" does not exist`, strings.Index(q, "missing")+1, "", "", "", "", "")
	q = "/* é */ INSERT INTO t VALUES (20),('oops')"
	check("prepare", q, "22P02", `invalid input syntax for type integer: "oops"`, utf8.RuneCountInString(q[:strings.Index(q, "'oops'")])+1, "", "", "", "", "")
	parameterError := referenceImplicitError("INSERT INTO t VALUES($1),($2)", referenceDiagnostic{
		Code: "22P02", Message: `invalid input syntax for type integer: "oops"`,
	}, 20, "oops")
	postgresParameterError := *parameterError.diagnostic
	postgresParameterError.Context = "unnamed portal parameter $2 = '...'"
	parameterError.diagnostic = &postgresParameterError
	parameterError.errorStage = "execute"
	p.step(t, parameterError)
	p.step(t, referenceSelect("SELECT * FROM t ORDER BY id", []string{"id", "big", "label", "flag"}, []string{"INT4", "INT8", "TEXT", "BOOL"}, [][]any{{int64(99), nil, nil, nil}}))
	p.step(t, referencePrepared(parameterError))
	p.step(t, referenceSelect("SELECT id FROM t ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(99)}}))
	q = "INSERT INTO t VALUES (20),(NULL)"
	check("execute", q, "23502", `null value in column "id" of relation "t" violates not-null constraint`, 0, "Failing row contains (null, null, null, null).", "", "public", "t", "id")
	p.step(t, referenceExec("CREATE TABLE required(id INT,flag BOOL NOT NULL)", 0))
	check("execute", "INSERT INTO required VALUES (1)", "23502", `null value in column "flag" of relation "required" violates not-null constraint`, 0, "Failing row contains (1, null).", "", "public", "required", "flag")
	p.step(t, referenceSelect("SELECT * FROM required", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{}))
	p.step(t, referenceExec("CREATE TABLE clip(label TEXT,flag BOOL,required INT NOT NULL)", 0))
	check("execute", "INSERT INTO clip VALUES ('raw',TRUE)", "23502", `null value in column "required" of relation "clip" violates not-null constraint`, 0, "Failing row contains (raw, t, null).", "", "public", "clip", "required")
	long := strings.Repeat("é", 33)
	clipParameterError := referenceImplicitError("INSERT INTO clip VALUES ($1,$2)", referenceDiagnostic{
		Code: "23502", Message: `null value in column "required" of relation "clip" violates not-null constraint`,
		Detail: "Failing row contains (" + strings.Repeat("é", 32) + "..., t, null).", Schema: "public", Table: "clip", Column: "required",
	}, long, true)
	clipParameterError.errorStage = "execute"
	p.step(t, referencePrepared(clipParameterError))
	p.step(t, referenceSelect("SELECT * FROM clip", []string{"label", "flag", "required"}, []string{"TEXT", "BOOL", "INT4"}, [][]any{}))
}
