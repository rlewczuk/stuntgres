package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwWriteNativeIdentity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader) {
	t.Helper()
	for _, hook := range []func(stuntgres.ForeignRowIdentity) stuntgres.ForeignRowIdentity{
		func(identity stuntgres.ForeignRowIdentity) stuntgres.ForeignRowIdentity { return nil },
		func(identity stuntgres.ForeignRowIdentity) stuntgres.ForeignRowIdentity {
			return stuntgres.ForeignRowIdentity{map[string]int{"bad": 1}}
		},
	} {
		r.writer.identityHook = hook
		fdwWriteFailedExec(t, p.Ctx, c, "UPDATE fdw_data SET label='wrong-target' WHERE id=1", nil)
		r.writer.identityHook = nil
		fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=1", [][]any{{int64(1), "one", true}}))
	}
	for _, value := range []driver.Value{
		fdwWriteIdentityDecimalSlice{7},
		fdwWriteIdentityDecimalParts{coefficient: []byte{7}},
		fdwWriteIdentityDecimalToken{coefficient: 7, metadata: []byte{1}},
	} {
		if !driver.IsValue(value) {
			t.Fatal("identity extension witness is not driver-valid", value)
		}
		r.writer.identityHook = func(stuntgres.ForeignRowIdentity) stuntgres.ForeignRowIdentity {
			return stuntgres.ForeignRowIdentity{value}
		}
		mutations := 0
		r.writer.writeHook = func(_ context.Context, name string) error {
			if name == "ExecForeignUpdate" {
				mutations++
			}
			return nil
		}
		result, err := c.ExecContext(p.Ctx, "UPDATE fdw_data SET label='wrong-target' WHERE id=1")
		r.writer.identityHook, r.writer.writeHook = nil, nil
		if result != nil || err == nil || err.Error() != "stuntgres: foreign row identity contains a noncomparable driver value" {
			t.Fatal("runtime-unhashable identity did not fail admission", result, err)
		}
		var diagnostic interface{ SQLState() string }
		if errors.As(err, &diagnostic) || mutations != 0 {
			t.Fatal("invalid identity reached mutation or invented SQLSTATE", mutations, err)
		}
		fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=1", [][]any{{int64(1), "one", true}}))
	}
}
func fdwWriteNativeDiagnostics(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_update_only_handler", 0))
	run(fixture.Exec("UPDATE fdw_data SET label='supported-update' WHERE id=1", 1))
	for _, tc := range []struct{ q, message string }{
		{"INSERT INTO fdw_data VALUES(4,'absent',true)", `cannot insert into foreign table "fdw_data"`},
		{"DELETE FROM fdw_data", `cannot delete from foreign table "fdw_data"`},
		{"TRUNCATE fdw_data", `cannot truncate foreign table "fdw_data"`},
	} {
		run(fixture.ErrorDiagnostic(tc.q, fdwDiagnostic("0A000", tc.message)))
	}
	run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_native HANDLER fdw_sql_handler", 0))
	want := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "P0001", Message: "provider rejected write", Detail: "provider detail", Hint: "provider hint", Position: "7", InternalPosition: "1", InternalQuery: "SELECT provider", Context: "provider context", Schema: "remote", Table: "source", Column: "label", DataType: "text", Constraint: "provider_constraint"}
	providerError := &stuntgres.Error{Severity: want.Severity, SeverityUnlocalized: want.SeverityUnlocalized, Code: want.Code, Message: want.Message, Detail: want.Detail, Hint: want.Hint, Position: want.Position, InternalPosition: want.InternalPosition, InternalQuery: want.InternalQuery, Context: want.Context, Schema: want.Schema, Table: want.Table, Column: want.Column, DataType: want.DataType, Constraint: want.Constraint}
	r.writer.writeHook = func(_ context.Context, name string) error {
		if name == "ExecForeignInsert" {
			return providerError
		}
		return nil
	}
	result, err := c.ExecContext(p.Ctx, "INSERT INTO fdw_data VALUES(4,'rejected',true)")
	r.writer.writeHook = nil
	if err == nil || result != nil {
		t.Fatal(result, err)
	}
	fixture.AssertDiagnostic(t, err, &want)
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=4", nil))
}

type fdwWriteIdentityDecimalSlice []byte

func (d fdwWriteIdentityDecimalSlice) Decompose([]byte) (byte, bool, []byte, int32) {
	return 0, false, d, 0
}

type fdwWriteIdentityDecimalParts struct{ coefficient []byte }

func (d fdwWriteIdentityDecimalParts) Decompose([]byte) (byte, bool, []byte, int32) {
	return 0, false, d.coefficient, 0
}

type fdwWriteIdentityDecimalToken struct {
	coefficient byte
	metadata    any
}

func (d fdwWriteIdentityDecimalToken) Decompose(buf []byte) (byte, bool, []byte, int32) {
	return 0, false, append(buf[:0], d.coefficient), 0
}
