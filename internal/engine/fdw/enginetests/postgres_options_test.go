package fdw_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/fdw"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestPostgresFDWOptions(t *testing.T) {
	for _, tc := range []struct {
		name, value   string
		expected      int32
		code, message string
	}{
		{"fetch_size", "010", 8, "", ""}, {"batch_size", "0x10", 16, "", ""},
		{"fetch_size", "1.5", 2, "", ""}, {"batch_size", "2.5", 2, "", ""},
		{"fetch_size", "2147483647", 2147483647, "", ""},
		{"fetch_size", "0.5", 0, "22023", `"fetch_size" must be an integer value greater than zero`},
		{"batch_size", "08", 0, "22023", `invalid value for integer option "batch_size": 08`},
		{"fetch_size", "2147483648", 0, "22023", `invalid value for integer option "fetch_size": 2147483648`},
	} {
		value, err := fdw.PostgresIntegerOption(tc.name, tc.value)
		if tc.code == "" {
			if err != nil || value != tc.expected {
				t.Fatal(tc, value, err)
			}
			continue
		}
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message}
		var d *sqlstate.Error
		if !errors.As(err, &d) || *d != want {
			t.Fatalf("integer %s=%s: got %#v want %#v", tc.name, tc.value, d, want)
		}
	}
	ctx := context.Background()
	for _, tc := range []struct {
		kind                catalog.ForeignObjectKind
		options             []catalog.ForeignOption
		super               bool
		code, message, hint string
	}{
		{kind: catalog.ForeignServer, options: []catalog.ForeignOption{{Name: "fdw_startup_cost", Value: "+Infinity"}}, super: true},
		{kind: catalog.ForeignUserMapping, options: []catalog.ForeignOption{{Name: "password_required", Value: "yes"}}, code: "42601", message: "password_required requires a Boolean value"},
		{kind: catalog.ForeignUserMapping, options: []catalog.ForeignOption{{Name: "password_required", Value: "false"}}, code: "42501", message: "password_required=false is superuser-only", hint: "User mappings with the password_required option set to false may only be created or modified by the superuser."},
		{kind: catalog.ForeignUserMapping, options: []catalog.ForeignOption{{Name: "sslcert", Value: ""}}, code: "42501", message: "sslcert and sslkey are superuser-only", hint: "User mappings with the sslcert or sslkey options set may only be created or modified by the superuser."},
		{kind: catalog.ForeignAttribute, options: []catalog.ForeignOption{{Name: "column_nam", Value: ""}}, code: "HV00D", message: `invalid option "column_nam"`, hint: `Perhaps you meant the option "column_name".`},
		{kind: catalog.ForeignWrapper, options: []catalog.ForeignOption{{Name: "fetch_size", Value: "1"}}, code: "HV00D", message: `invalid option "fetch_size"`, hint: "There are no valid options in this context."},
	} {
		before := slices.Clone(tc.options)
		err := fdw.ValidatePostgresOptions(ctx, tc.kind, tc.options, fdw.PostgresPrivileges{Superuser: tc.super})
		if !reflect.DeepEqual(before, tc.options) {
			t.Fatal("validator rewrote input options")
		}
		if tc.code == "" {
			if err != nil {
				t.Fatal(err)
			}
			continue
		}
		var d *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Hint: tc.hint}
		if !errors.As(err, &d) || *d != want {
			t.Fatalf("option diagnostics got %#v want %#v", d, want)
		}
	}
	var text, array *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Text {
			text = typ
		}
		if typ.ID() == 1009 {
			array = typ
		}
	}
	values := []sqltype.Value{sqltype.String(text, "invalid_zzzz=x"), sqltype.Null(text)}
	input, err := sqltype.OwnedArrayValue(array, []sqltype.ArrayDimension{{Lower: -3, Length: 2}}, values)
	if err != nil {
		t.Fatal(err)
	}
	var d *sqlstate.Error
	if err := fdw.ValidatePostgresOptionArray(ctx, catalog.ForeignServer, input, fdw.PostgresPrivileges{Superuser: true}); !errors.As(err, &d) || d.Code != "22004" {
		t.Fatal("NULL prepass lost precedence", err)
	}
	if input.ArrayDimension(0).Lower != -3 || input.ArrayElement(0) != values[0] {
		t.Fatal("array decoder changed owned backing")
	}
	var notices []sqlstate.Error
	privileges := fdw.PostgresPrivileges{Superuser: true, Installed: func(name string) bool { return name == "installed" }, Notice: func(d sqlstate.Error) { notices = append(notices, d) }}
	if err := fdw.ValidatePostgresOptions(ctx, catalog.ForeignServer, []catalog.ForeignOption{{Name: "extensions", Value: `installed,foo;bar,"Mixed ""Quote"`}}, privileges); err != nil {
		t.Fatal(err)
	}
	want := []sqlstate.Error{{Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42704", Message: `extension "foo;bar" is not installed`}, {Severity: "WARNING", SeverityUnlocalized: "WARNING", Code: "42704", Message: `extension "Mixed "Quote" is not installed`}}
	if !reflect.DeepEqual(notices, want) {
		t.Fatal("ordered warning records changed", notices)
	}
	options := []catalog.ForeignOption{{Name: "fetch_size", Value: "1"}, {Name: "host", Value: "actual"}, {Name: "password_required", Value: "false"}, {Name: "password", Value: "sensitive"}}
	filtered := fdw.PostgresTransportOptions(options)
	if !reflect.DeepEqual(filtered, []catalog.ForeignOption{options[1], options[3]}) {
		t.Fatal("stock transport filter lost order or forwarded FDW options")
	}
	filtered[0].Value = "changed"
	if options[1].Value != "actual" {
		t.Fatal("transport filter borrowed option backing")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := fdw.ValidatePostgresOptions(canceled, catalog.ForeignServer, options, privileges); !errors.Is(err, context.Canceled) {
		t.Fatal("validator lost cancellation", err)
	}
}
