package fdw

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestFileFDWOptions(t *testing.T) {
	privileged := FilePrivileges{ReadFiles: true, ExecutePrograms: true}
	for _, tc := range []struct { name string; kind catalog.ForeignObjectKind; options []parser.CopyOption; privileges FilePrivileges; code, message, detail, hint string }{
		{name: "empty filename", kind: catalog.ForeignTable, options: []parser.CopyOption{{Name: "filename", HasValue: true}}, privileges: privileged},
		{name: "empty program", kind: catalog.ForeignTable, options: []parser.CopyOption{{Name: "program", HasValue: true}}, privileges: privileged},
		{name: "missing", kind: catalog.ForeignTable, privileges: privileged, code: "HV002", message: "either filename or program is required for file_fdw foreign tables"},
		{name: "format before missing", kind: catalog.ForeignTable, options: []parser.CopyOption{{Name: "format", Value: "CSV", HasValue: true}}, privileges: privileged, code: "22023", message: `COPY format "CSV" not recognized`},
		{name: "source before format", kind: catalog.ForeignTable, options: []parser.CopyOption{{Name: "filename", Value: "/x", HasValue: true}, {Name: "format", Value: "CSV", HasValue: true}}, code: "42501", message: `permission denied to set the "filename" option of a file_fdw foreign table`, detail: `Only roles with privileges of the "pg_read_server_files" role may set this option.`},
		{name: "omitted filename", kind: catalog.ForeignTable, options: []parser.CopyOption{{Name: "filename"}}, privileges: privileged, code: "42601", message: "filename requires a parameter"},
		{name: "forces coexist", kind: catalog.ForeignAttribute, options: []parser.CopyOption{{Name: "force_null"}, {Name: "force_not_null", Value: "off", HasValue: true}}},
		{name: "string header numeric", kind: catalog.ForeignTable, options: []parser.CopyOption{{Name: "header", Value: "1", HasValue: true}}, privileges: privileged, code: "42601", message: `header requires a Boolean value or "match"`},
		{name: "string force numeric", kind: catalog.ForeignAttribute, options: []parser.CopyOption{{Name: "force_null", Value: "1", HasValue: true}}, code: "42601", message: "force_null requires a Boolean value"},
		{name: "duplicate force", kind: catalog.ForeignAttribute, options: []parser.CopyOption{{Name: "force_null"}, {Name: "force_null"}}, code: "42601", message: "conflicting or redundant options", hint: `Option "force_null" supplied more than once for a column.`},
		{name: "no server options", kind: catalog.ForeignServer, options: []parser.CopyOption{{Name: "filename", Value: "/x", HasValue: true}}, code: "HV00D", message: `invalid option "filename"`, hint: "There are no valid options in this context."},
		{name: "released vocabulary", kind: catalog.ForeignTable, options: []parser.CopyOption{{Name: "reject_limi"}}, code: "HV00D", message: `invalid option "reject_limi"`, hint: `Perhaps you meant the option "reject_limit".`},
		{name: "bounded self suggestion", kind: catalog.ForeignTable, options: []parser.CopyOption{{Name: "default", Value: "x", HasValue: true}}, code: "HV00D", message: `invalid option "default"`, hint: `Perhaps you meant the option "default".`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := slices.Clone(tc.options)
			err := validateFileOptions(context.Background(), tc.kind, tc.options, tc.privileges)
			if !reflect.DeepEqual(tc.options, before) { t.Fatal("parser mutated input backing") }
			if tc.code == "" { if err != nil { t.Fatal(err) }; return }
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Detail: tc.detail, Hint: tc.hint}
			var diagnostic *sqlstate.Error
			if !errors.As(err, &diagnostic) || *diagnostic != want { t.Fatalf("got %#v want %+v", err, want) }
		})
	}
	var text, array *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() { if typ.ID() == 25 { text = typ }; if typ.ID() == 1009 { array = typ } }
	values := []sqltype.Value{sqltype.String(text, "filename=a=b"), sqltype.String(text, "format=csv")}
	input, err := sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: -3, Length: 2}}, values)
	if err != nil { t.Fatal(err) }
	if err := ValidateFileOptionArray(context.Background(), catalog.ForeignTable, input, privileged); err != nil { t.Fatal(err) }
	if input.ArrayElement(0) != values[0] || input.ArrayDimension(0).Lower != -3 { t.Fatal("decoder mutated typed array") }
	nullInput, err := sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: 1, Length: 1}}, []sqltype.Value{sqltype.Null(text)})
	if err != nil { t.Fatal(err) }
	var diagnostic *sqlstate.Error
	if err := ValidateFileOptionArray(context.Background(), catalog.ForeignTable, nullInput, privileged); !errors.As(err, &diagnostic) || diagnostic.Code != "22004" { t.Fatal("NULL element", err) }
	options := []catalog.ForeignOption{{Name: "filename", Value: "/not-a-real-unit-file"}, {Name: "format", Value: "csv"}}
	before := slices.Clone(options)
	if err := ValidateFileOptions(context.Background(), catalog.ForeignTable, options, privileged); err != nil { t.Fatal(err) }
	if !reflect.DeepEqual(options, before) { t.Fatal("DDL parser mutated owned catalog options") }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ValidateFileOptions(ctx, catalog.ForeignTable, options, privileged); !errors.Is(err, context.Canceled) { t.Fatal(err) }
}
