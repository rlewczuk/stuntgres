package core_test

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"stuntgres/internal/workadmission"
	"testing"
)

func TestSepgsqlSimulationConversions(t *testing.T) {
	for _, label := range []string{"S", "U"} {
		t.Run(label, func(t *testing.T) {
			e, _ := sepgsqlCodecEngine(t, "S")
			enginetest.Run(t, e, "SECURITY LABEL ON FUNCTION pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean) IS '"+label+"'")
			op, done := enginetest.RoleOperation(t, e, "postgres")
			t.Cleanup(done)
			enginetest.RoleExecute(t, e, op, "SELECT sepgsql_setcon('R')")
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { scope.Finish(nil) })
			if err := scope.Gate(enginetest.Statement(t, "SELECT 1")); err != nil {
				t.Fatal(err)
			}
			e.Mu.Lock()
			auth, err := scope.RoutineAuthorizationLocked()
			if err == nil {
				err = scope.CaptureIdentityLocked(auth)
			}
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			read := auth.Catalog
			conversion, ok := read.Conversion(ids.ObjectAddress{ClassID: ids.ConversionClassID, ObjectID: 4519})
			if !ok {
				t.Fatal("missing genuine UTF8/LATIN1 conversion")
			}
			helper, ok := read.Procedure(conversion.Handler)
			if !ok {
				t.Fatal("missing genuine conversion helper")
			}
			callback, err := catalog.CaptureEncodingConversion(read, conversion, helper)
			if err != nil {
				t.Fatal(err)
			}
			selected := sqltype.NewEncodingConversion(6, 8, helper.Conversion.WithReference(callback))
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: op.Context(), Host: core.HostOf(scope), Catalog: read}}
			// Selection and no-work paths must not enter a helper or demand a borrower.
			if value, err := selected.ConvertWithBoundary(op.Context(), "", nil); err != nil || value != "" {
				t.Fatal("empty conversion demanded invocation", value, err)
			}
			if value, err := sqltype.NewEncodingConversion(6, 6, helper.Conversion.WithReference(callback)).Convert(op.Context(), "é"); err != nil || value != "é" {
				t.Fatal("identity conversion invoked callback", value, err)
			}
			if _, err := selected.Convert(op.Context(), "é"); err == nil {
				t.Fatal("missing borrower executed selected callback")
			}
			output, err := selected.ConvertWithBoundary(op.Context(), "é", &evaluation)
			if label == "U" {
				enginetest.Diagnostic(t, err, sqlstate.Error{Code: "42501", Message: "SELinux: security policy violation"})
			} else if err != nil || output != "\xe9" {
				t.Fatal("real converted bytes", output, err)
			}
			before := []byte("prefix")
			outputBytes, err := selected.AppendWithBoundary(op.Context(), before, "€", &evaluation)
			if string(outputBytes) != "prefix" || err == nil {
				t.Fatal("failed append exposed a datum prefix", outputBytes, err)
			}
			if label == "U" {
				enginetest.State(t, err, "42501")
			} else {
				enginetest.State(t, err, "22P05")
			}
			if op.Session.Sepgsql.Current() != "R" {
				t.Fatal("conversion failed to unwind lexical subject")
			}
			foreign := callback
			foreign.Ref = read.Domain().Ref(read.Epoch(), ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 1258})
			if _, err = evaluation.EnterEncodingConversion(foreign); err == nil {
				t.Fatal("foreign routine identity entered converter")
			}
			canceled, cancel := context.WithCancel(op.Context())
			cancel()
			if _, err = selected.ConvertWithBoundary(canceled, "é", &evaluation); !errors.Is(err, context.Canceled) {
				t.Fatal("canceled conversion entered kernel", err)
			}
			if label == "S" {
				reached := false
				during, cancelDuring := context.WithCancel(op.Context())
				during = workadmission.Activate(workadmission.With(during, func(_ context.Context, class workadmission.Class) error {
					if class != workadmission.WorkEncodingConvert {
						return nil
					}
					reached = true
					if op.Session.Sepgsql.Current() != "T" {
						t.Fatal("actual converter quantum ran outside its trusted body")
					}
					cancelDuring()
					return context.Canceled
				}))
				partial, err := selected.AppendWithBoundary(during, []byte("prefix"), strings.Repeat("é", 8192), &evaluation)
				cancelDuring()
				if !reached || !errors.Is(err, context.Canceled) || string(partial) != "prefix" || op.Session.Sepgsql.Current() != "R" {
					t.Fatal("in-kernel cancellation leaked prefix or subject", reached, string(partial), err)
				}
			}
			enginetest.Run(t, e, "ALTER FUNCTION pg_catalog.utf8_to_iso8859_1(integer,integer,cstring,internal,integer,boolean) RENAME TO sepgsql_stale_converter")
			if _, err = selected.ConvertWithBoundary(op.Context(), "é", &evaluation); err == nil {
				t.Fatal("stale selected conversion helper silently adopted its replacement")
			}
			if op.Session.Sepgsql.Current() != "R" {
				t.Fatal("stale converter entry leaked trusted subject")
			}
			scope.Finish(nil)
			if _, err = selected.ConvertWithBoundary(op.Context(), "é", &evaluation); err == nil {
				t.Fatal("ended statement retained conversion authority")
			}
		})
	}
}
