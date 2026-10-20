package engine

import (
	"context"
	"errors"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"testing"
)

func tablespaceUnitDiagnostic(t *testing.T, err error, code, message, detail string) {
	t.Helper()
	if code == "" {
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	var d *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Detail: detail}
	if !errors.As(err, &d) || *d != want {
		t.Fatalf("diagnostic got %#v (%v), want %#v", d, err, want)
	}
}

func TestTablespaceNameValidation(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		existing              bool
		code, message, detail string
	}{
		{"ts_existing", true, "42710", "tablespace \"ts_existing\" already exists", ""},
		{"ts_existing", false, "", "", ""},
		{"pg_default", true, "42939", "unacceptable tablespace name \"pg_default\"", "The prefix \"pg_\" is reserved for system tablespaces."},
		{"pg_missing", false, "42939", "unacceptable tablespace name \"pg_missing\"", "The prefix \"pg_\" is reserved for system tablespaces."},
		{"PG_custom", false, "", "", ""},
		{"PG_custom", true, "42710", "tablespace \"PG_custom\" already exists", ""},
	} {
		tablespaceUnitDiagnostic(t, tablespaceName(tc.name, tc.existing), tc.code, tc.message, tc.detail)
	}
}

func TestTablespaceCanonicalPaths(t *testing.T) {
	for _, tc := range []struct{ input, canonical, code, message string }{
		{"", "", "42P17", "tablespace location must be an absolute path"},
		{" ", " ", "42P17", "tablespace location must be an absolute path"},
		{"a/../b", "b", "42P17", "tablespace location must be an absolute path"},
		{"/a//./b/../c/", "/a/c", "", ""},
		{"/../../", "/", "", ""},
		{"/bad'quote/../valid", "/valid", "", ""},
		{"/bad'quote", "/bad'quote", "42602", "tablespace location cannot contain single quotes"},
		{"bad'quote", "bad'quote", "42602", "tablespace location cannot contain single quotes"},
		{"/a\\b", "/a\\b", "", ""},
	} {
		got, err := tablespaceLocation(tc.input)
		if got != tc.canonical {
			t.Fatal(tc.input, got, tc.canonical)
		}
		tablespaceUnitDiagnostic(t, err, tc.code, tc.message, "")
	}
	for _, size := range []int{970, 971} {
		for _, multibyte := range []bool{false, true} {
			location := "/" + strings.Repeat("a", size-1)
			if multibyte {
				location = "/" + strings.Repeat("é", (size-1)/2) + strings.Repeat("z", (size-1)%2)
			}
			if len(location) != size {
				t.Fatal("incorrect byte fixture")
			}
			canonical, err := tablespaceLocation(location)
			if canonical != location {
				t.Fatal("changed clean path")
			}
			if size == 970 {
				tablespaceUnitDiagnostic(t, err, "", "", "")
			} else {
				tablespaceUnitDiagnostic(t, err, "42P17", "tablespace location \""+location+"\" is too long", "")
			}
		}
	}
	location := "/" + strings.Repeat("x/../", 300) + "valid"
	if got, err := tablespaceLocation(location); got != "/valid" || err != nil {
		t.Fatal("length must follow canonicalization", got, err)
	}
}

func TestTablespaceExecutorNoPublication(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	state, roles, oid, revision, principal := e.catalog.state, e.roles.published, e.catalog.nextOID, e.catalog.nextRevision, e.roles.nextPrincipal
	identity, authorizationRevision := op.RoleState(), op.Session.authorization.revision
	for _, tc := range []struct {
		stmt          parser.CreateTablespace
		code, message string
	}{
		{parser.CreateTablespace{Name: parser.Identifier{Text: "ordinary"}, Location: "/valid"}, "0A000", tablespacePhysicalUnsupported},
		{parser.CreateTablespace{Name: parser.Identifier{Text: "ordinary"}, Location: "relative"}, "42P17", "tablespace location must be an absolute path"},
		{parser.CreateTablespace{Name: parser.Identifier{Text: "ordinary"}, Owner: &parser.RoleSpec{Name: "missing"}, Location: "relative"}, "42704", "role \"missing\" does not exist"},
	} {
		result, err := e.Execute(op, &tc.stmt, 0, nil)
		tablespaceUnitDiagnostic(t, err, tc.code, tc.message, "")
		if result.Rows != nil || result.CommandTag != "" {
			t.Fatal("refusal exposed result", result)
		}
		if e.catalog.state != state || e.catalog.state.Epoch() != state.Epoch() || e.roles.published != roles || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || e.roles.nextPrincipal != principal || op.RoleState() != identity || op.Session.authorization.revision != authorizationRevision {
			t.Fatal("refusal published catalog/identity/allocator changes")
		}
	}
	original := op.Ctx
	ctx, cancel := context.WithCancel(original)
	cancel()
	op.Ctx = ctx
	_, err := e.Execute(op, &parser.CreateTablespace{Name: parser.Identifier{Text: "ordinary"}, Location: "/valid"}, 0, nil)
	op.Ctx = original
	if !errors.Is(err, context.Canceled) || e.catalog.state != state || e.roles.published != roles || e.catalog.nextOID != oid || e.catalog.nextRevision != revision || e.roles.nextPrincipal != principal {
		t.Fatal("canceled create published", err)
	}
}
