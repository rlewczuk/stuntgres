package enginetests

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestDDLCommandQueryOwnership(t *testing.T) {
	find := func() *sqltype.Descriptor {
		for _, d := range sqltype.Builtins() {
			if d.ID() == 32 {
				return d
			}
		}
		t.Fatal("missing canonical pg_ddl_command")
		return nil
	}
	d, fresh := find(), find()
	command := sqltype.DDLCommand{Tag: "ALTER TABLE", ClassID: 1259, ObjectID: 50000, AlterActions: 2}
	v, err := sqltype.DDLCommandValue(d, command)
	if err != nil || v.IsNull() || !v.ValidResult(d.Usage()) {
		t.Fatal(v, err)
	}
	copy := v
	command.Tag = "changed caller"
	if v != copy {
		t.Fatal("caller mutation changed owned datum")
	}
	backing := reflect.ValueOf(v).FieldByName("ddlCommand").Pointer()
	if backing == 0 {
		t.Fatal("opaque command has no immutable backing")
	}
	next, err := v.Retag(fresh)
	if err != nil || next.IsNull() || !next.ValidResult(fresh.Usage()) || reflect.ValueOf(next).FieldByName("ddlCommand").Pointer() != backing {
		t.Fatal("reownership lost immutable payload", err)
	}
	for _, forged := range []*sqltype.Descriptor{sqltype.LookupOnly(32, "pg_ddl_command", 0), func() *sqltype.Descriptor { c := *d; return &c }()} {
		if _, err := sqltype.DDLCommandValue(forged, command); err == nil {
			t.Fatal("forged pseudo descriptor accepted")
		}
	}
	var diagnostic *sqlstate.Error
	if err := sqltype.CheckStoredColumn("command", d.Usage()); !errors.As(err, &diagnostic) || diagnostic.Code != "42P16" {
		t.Fatal("query carrier gained storage authority", err)
	}
	if !sqltype.Null(d).IsNull() || !sqltype.Null(d).ValidResult(d.Usage()) {
		t.Fatal("typed NULL regressed")
	}
	_, err = d.Input(context.Background(), "x", -1)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "cannot accept a value of type pg_ddl_command" {
		t.Fatal("opaque input", err)
	}
	_, err = d.Output(context.Background(), v)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "cannot display a value of type pg_ddl_command" {
		t.Fatal("opaque output", err)
	}
	_, err = d.AppendBinaryWithEnvironment(context.Background(), nil, v, sqltype.OutputEnvironment{})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "cannot display a value of type pg_ddl_command" {
		t.Fatal("opaque binary output", err)
	}
	_, err = d.BinaryInputWithEnvironment(context.Background(), nil, -1, sqltype.InputEnvironment{})
	if !errors.As(err, &diagnostic) || diagnostic.Code != "0A000" || diagnostic.Message != "cannot accept a value of type pg_ddl_command" {
		t.Fatal("opaque binary input", err)
	}
	shape, err := sqltype.RecordShape([]sqltype.CompositeMember{{Attribute: 1, Name: "command", Usage: d.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	fields := []sqltype.Value{v}
	record, err := sqltype.CompositeValue(shape, fields)
	if err != nil {
		t.Fatal(err)
	}
	fields[0] = sqltype.Null(d)
	if record.CompositeField(0).IsNull() || !sqltype.SameDatum(record.CompositeField(0), v) {
		t.Fatal("composite borrowed caller storage or erased nonNULL datum")
	}
	same, err := v.SamePayload(context.Background(), next)
	if err != nil || !same {
		t.Fatal("compatible reownership changed payload", err)
	}
}
