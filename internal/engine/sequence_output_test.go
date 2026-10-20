package engine

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestSequenceRegclassOutputAndNumericIdentity(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE SEQUENCE s START 10")
	ast, d := sequencePreparedDescription(t, e, s, "SELECT 's'::regclass")
	oid := d.sequences.Constants[0].Ref.Address.ObjectID
	r, err := sequencePreparedExecute(e, s, ast, &d)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Rows.Close()
	if r.Rows.Schema()[0].Name != "regclass" {
		t.Fatal(r.Rows.Schema())
	}
	numeric, nd := sequencePreparedDescription(t, e, s, fmt.Sprintf("SELECT nextval('%d'::regclass)", oid))
	parameter, pd := sequencePreparedDescription(t, e, s, "SELECT nextval($1)")
	param := func() (Result, error) {
		op, err := s.Acquire(context.Background())
		if err != nil {
			return Result{}, err
		}
		defer op.Done()
		scope, err := e.StartStatement(op)
		if err != nil {
			return Result{}, err
		}
		scope.Expected = &pd
		r, err := scope.Execute(parameter, 1, func(ctx context.Context, info analyze.ParameterInfo, frame []sqltype.Value, input sqltype.InputEnvironment) error {
			var err error
			frame[0], err = info.Types[0].Type.InputWithEnvironment(ctx, fmt.Sprint(oid), -1, input)
			return err
		})
		err = scope.Finish(err)
		return r, err
	}
	sessionSQL(t, s, "ALTER SEQUENCE s RENAME TO identity")
	frame := make([]sqltype.Value, 1)
	if present, err := r.Rows.Next(context.Background(), frame); err != nil || !present {
		t.Fatal(present, err)
	}
	if frame[0].Type().ID() != sqltype.Regclass {
		t.Fatal("internal datum lost OID type")
	}
	if text, err := frame[0].Text(); err != nil || text != fmt.Sprint(oid) {
		t.Fatal(text, err)
	}
	if text, err := r.Rows.RegclassText(frame[0]); err != nil || text != "s" {
		t.Fatal("retained presentation changed", text, err)
	}
	result, err := sequencePreparedExecute(e, s, numeric, &nd)
	if err != nil {
		t.Fatal(err)
	}
	if got := tuples(result); got[0][0] != int64(10) {
		t.Fatal(got)
	}
	result, err = param()
	if err != nil {
		t.Fatal(err)
	}
	if got := tuples(result); got[0][0] != int64(11) {
		t.Fatal(got)
	}
	sessionSQL(t, s, "DROP SEQUENCE identity")
	sessionSQL(t, s, "CREATE SEQUENCE s START 90")
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "XX000", Message: fmt.Sprintf("could not open relation with OID %d", oid)}
	_, err = sequencePreparedExecute(e, s, numeric, &nd)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatalf("literal got=%#v want=%#v", diagnostic, want)
	}
	_, err = param()
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatalf("parameter got=%#v want=%#v", diagnostic, want)
	}
	sequenceScalar(t, s, "SELECT nextval('s')", 90)
}
