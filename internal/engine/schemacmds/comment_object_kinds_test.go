package schemacmds_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// commentObjectRef resolves a public catalog object by its name key.
func commentObjectRef(t *testing.T, e *core.Engine, class ids.OID, name, signature string) ids.ObjectRef {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	read := e.Catalog.Current()
	namespace, ok := read.LookupName(ids.NameKey{ClassID: ids.NamespaceClassID, Name: "public"})
	if !ok {
		t.Fatal("public schema is missing")
	}
	address, ok := read.LookupName(ids.NameKey{ClassID: class, Namespace: namespace, Name: name, Signature: signature})
	if !ok {
		t.Fatalf("catalog object class %d %q is missing", class, name)
	}
	return e.Catalog.Domain.Ref(read.Epoch(), address)
}

func commentTriggerRef(t *testing.T, e *core.Engine, table ids.ObjectRef, name string) (ids.ObjectRef, ids.OID) {
	t.Helper()
	e.Mu.Lock()
	defer e.Mu.Unlock()
	read := e.Catalog.Current()
	relation := read.MustRelation(table.Address)
	oid, ok := catalog.TriggerOIDByName(read, relation, name)
	if !ok {
		t.Fatalf("trigger %q is not live on %s", name, relation.Name())
	}
	return e.Catalog.Domain.Ref(read.Epoch(), ids.ObjectAddress{ClassID: ids.TriggerClassID, ObjectID: oid}), oid
}

// commentDescription reads the single objsubid 0 pg_description row of one
// catalog object.
func commentDescription(t *testing.T, e *core.Engine, op *session.Operation, class ids.OID, oid ids.OID) (string, bool) {
	t.Helper()
	result := enginetest.RoleExecute(t, e, op, fmt.Sprintf("SELECT description FROM pg_catalog.pg_description WHERE classoid = %d AND objoid = %d AND objsubid = 0", class, oid))
	values := make([]sqltype.Value, 1)
	present, err := result.Rows.Next(context.Background(), values)
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		return "", false
	}
	text, err := values[0].Text()
	if err != nil {
		t.Fatal(err)
	}
	if again, err := result.Rows.Next(context.Background(), values); err != nil || again {
		t.Fatalf("duplicate pg_description row for %d/%d: %v %v", class, oid, again, err)
	}
	return text, true
}

func commentObjectDiagnostic(t *testing.T, e *core.Engine, op *session.Operation, q string, want sqlstate.Error) {
	t.Helper()
	before := e.CatalogRead()
	_, err := e.Execute(op, enginetest.Statement(t, q), 0, nil)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) {
		t.Fatalf("%s: %T %v", q, err, err)
	}
	if !reflect.DeepEqual(*diagnostic, want) {
		t.Fatalf("%s: %#v want %#v", q, *diagnostic, want)
	}
	if before.State() != e.CatalogRead().State() {
		t.Fatalf("%s: failed COMMENT published metadata", q)
	}
}

func commentObjectError(code, message string) sqlstate.Error {
	return sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message}
}

func TestCommentObjectKinds(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE TYPE comment_kind_enum AS ENUM ('a','b')",
		"CREATE DOMAIN comment_kind_domain AS integer",
		"CREATE TABLE comment_kind_table(id integer, v text)",
		"CREATE INDEX comment_kind_index ON comment_kind_table(id)",
		"CREATE FUNCTION comment_kind_fn(integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN $1; END$$",
		"CREATE PROCEDURE comment_kind_proc(integer) LANGUAGE plpgsql AS $$BEGIN END$$",
		"CREATE FUNCTION comment_kind_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
		"CREATE TRIGGER comment_kind_trg AFTER INSERT ON comment_kind_table FOR EACH ROW EXECUTE PROCEDURE comment_kind_trigger()",
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	enumRef := commentObjectRef(t, e, ids.TypeClassID, "comment_kind_enum", "")
	domainRef := commentObjectRef(t, e, ids.TypeClassID, "comment_kind_domain", "")
	rowTypeRef := commentObjectRef(t, e, ids.TypeClassID, "comment_kind_table", "")
	tableRef := commentObjectRef(t, e, ids.RelationClassID, "comment_kind_table", "")
	indexRef := commentObjectRef(t, e, ids.RelationClassID, "comment_kind_index", "")
	fnRef := commentObjectRef(t, e, ids.ProcedureClassID, "comment_kind_fn", "23")
	procRef := commentObjectRef(t, e, ids.ProcedureClassID, "comment_kind_proc", "23")
	triggerRef, triggerOID := commentTriggerRef(t, e, tableRef, "comment_kind_trg")

	old := e.CatalogRead()
	enumDescriptor, ok := old.Find(enumRef.Address)
	if !ok {
		t.Fatal("missing enum descriptor")
	}
	fnDescriptor, ok := old.Find(fnRef.Address)
	if !ok {
		t.Fatal("missing function descriptor")
	}
	tableDescriptor, ok := old.Find(tableRef.Address)
	if !ok {
		t.Fatal("missing table descriptor")
	}
	enumRevision, fnRevision, tableRevision := enumDescriptor.Revision(), fnDescriptor.Revision(), tableDescriptor.Revision()
	for _, q := range []string{
		"COMMENT ON TYPE comment_kind_enum IS 'enum'",
		"COMMENT ON DOMAIN comment_kind_domain IS 'domain'",
		"COMMENT ON TYPE comment_kind_table IS 'row type'",
		"COMMENT ON INDEX comment_kind_index IS 'index'",
		"COMMENT ON FUNCTION comment_kind_fn(integer) IS 'function'",
		"COMMENT ON PROCEDURE comment_kind_proc(integer) IS 'procedure'",
		"COMMENT ON TRIGGER comment_kind_trg ON comment_kind_table IS 'trigger'",
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	for _, tc := range []struct {
		name string
		ref  ids.ObjectRef
		text string
	}{
		{"enum", enumRef, "enum"},
		{"domain", domainRef, "domain"},
		{"row type", rowTypeRef, "row type"},
		{"index", indexRef, "index"},
		{"function", fnRef, "function"},
		{"procedure", procRef, "procedure"},
		{"trigger", triggerRef, "trigger"},
	} {
		commentWant(t, e, e.CatalogRead(), tc.ref, tc.text)
		commentWant(t, e, old, tc.ref, "")
	}
	current := e.CatalogRead()
	for _, tc := range []struct {
		name  string
		ref   ids.ObjectRef
		class ids.OID
		text  string
	}{
		{"enum", enumRef, ids.TypeClassID, "enum"},
		{"domain", domainRef, ids.TypeClassID, "domain"},
		{"index", indexRef, ids.RelationClassID, "index"},
		{"function", fnRef, ids.ProcedureClassID, "function"},
		{"trigger", triggerRef, ids.TriggerClassID, "trigger"},
	} {
		text, present := commentDescription(t, e, op, tc.class, tc.ref.Address.ObjectID)
		if !present || text != tc.text {
			t.Fatalf("%s pg_description: %q %v want %q", tc.name, text, present, tc.text)
		}
	}
	if stored := current.MustRelation(tableRef.Address).TriggerComments[triggerOID]; stored != "trigger" {
		t.Fatalf("relation descriptor trigger comment: %q", stored)
	}
	if updated, drawn := current.Find(enumRef.Address); !drawn || updated.Revision() <= enumRevision {
		t.Fatalf("type revision did not advance: %v <= %d", updated, enumRevision)
	}
	if updated, drawn := current.Find(fnRef.Address); !drawn || updated.Revision() <= fnRevision {
		t.Fatalf("procedure revision did not advance: %v <= %d", updated, fnRevision)
	}
	if updated, drawn := current.Find(tableRef.Address); !drawn || updated.Revision() <= tableRevision {
		t.Fatalf("relation revision did not advance: %v <= %d", updated, tableRevision)
	}

	// Replacement, NULL and empty removal, and a space that persists.
	for _, tc := range []struct {
		set    string
		ref    ids.ObjectRef
		text   string
		remove string
	}{
		{"COMMENT ON TYPE comment_kind_enum IS 'replacement'", enumRef, "replacement", "COMMENT ON TYPE comment_kind_enum IS NULL"},
		{"COMMENT ON INDEX comment_kind_index IS 'replacement index'", indexRef, "replacement index", "COMMENT ON INDEX comment_kind_index IS ''"},
		{"COMMENT ON TRIGGER comment_kind_trg ON comment_kind_table IS 'replacement trigger'", triggerRef, "replacement trigger", "COMMENT ON TRIGGER comment_kind_trg ON comment_kind_table IS NULL"},
		{"COMMENT ON FUNCTION comment_kind_fn(integer) IS ' '", fnRef, " ", "COMMENT ON ROUTINE comment_kind_fn(integer) IS NULL"},
	} {
		enginetest.RoleExecute(t, e, op, tc.set)
		commentWant(t, e, e.CatalogRead(), tc.ref, tc.text)
		enginetest.RoleExecute(t, e, op, tc.remove)
		commentWant(t, e, e.CatalogRead(), tc.ref, "")
	}
	// Re-issuing NULL on an uncommented object succeeds and changes nothing.
	enginetest.RoleExecute(t, e, op, "COMMENT ON INDEX comment_kind_index IS NULL")
	commentWant(t, e, e.CatalogRead(), indexRef, "")
	// ROUTINE accepts both routine kinds and the written kind only changes
	// diagnostics, not the target.
	enginetest.RoleExecute(t, e, op, "COMMENT ON ROUTINE comment_kind_proc(integer) IS 'routine procedure'")
	commentWant(t, e, e.CatalogRead(), procRef, "routine procedure")
	enginetest.RoleExecute(t, e, op, "COMMENT ON ROUTINE comment_kind_proc(integer) IS NULL")
	commentWant(t, e, e.CatalogRead(), procRef, "")

	// The domain comment may clear DomainPlanStamp (analysis invalidation
	// only); the domain keeps its base type and remains commentable.
	enginetest.RoleExecute(t, e, op, "COMMENT ON DOMAIN comment_kind_domain IS 'domain replacement'")
	commentWant(t, e, e.CatalogRead(), domainRef, "domain replacement")
	enginetest.RoleExecute(t, e, op, "CREATE TABLE comment_kind_domain_use(v comment_kind_domain)")
	enginetest.RoleExecute(t, e, op, "INSERT INTO comment_kind_domain_use VALUES (1)")
	commentWant(t, e, e.CatalogRead(), domainRef, "domain replacement")
}

func TestCommentObjectKindsDiagnostics(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE TYPE comment_kind_enum AS ENUM ('a','b')",
		"CREATE DOMAIN comment_kind_domain AS integer",
		"CREATE TABLE comment_kind_table(id integer, v text)",
		"CREATE INDEX comment_kind_index ON comment_kind_table(id)",
		"CREATE FUNCTION comment_kind_fn(integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN $1; END$$",
		"CREATE FUNCTION comment_kind_over(integer) RETURNS integer LANGUAGE plpgsql AS $$BEGIN RETURN $1; END$$",
		"CREATE FUNCTION comment_kind_over(text) RETURNS text LANGUAGE plpgsql AS $$BEGIN RETURN $1; END$$",
		"CREATE PROCEDURE comment_kind_proc(integer) LANGUAGE plpgsql AS $$BEGIN END$$",
		"CREATE FUNCTION comment_kind_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
		"CREATE TRIGGER comment_kind_trg AFTER INSERT ON comment_kind_table FOR EACH ROW EXECUTE PROCEDURE comment_kind_trigger()",
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	for _, tc := range []struct {
		query string
		want  sqlstate.Error
	}{
		{"COMMENT ON TYPE nosuch IS NULL", commentObjectError("42704", `type "nosuch" does not exist`)},
		{"COMMENT ON DOMAIN nosuchdom IS NULL", commentObjectError("42704", `type "nosuchdom" does not exist`)},
		{"COMMENT ON DOMAIN comment_kind_enum IS NULL", commentObjectError("42809", `"comment_kind_enum" is not a domain`)},
		{"COMMENT ON TYPE nosuchschema.t IS NULL", commentObjectError("3F000", `schema "nosuchschema" does not exist`)},
		{"COMMENT ON INDEX nosuchix IS NULL", commentObjectError("42P01", `relation "nosuchix" does not exist`)},
		{"COMMENT ON INDEX nosuchschema.nosuchix IS NULL", commentObjectError("3F000", `schema "nosuchschema" does not exist`)},
		{"COMMENT ON INDEX comment_kind_table IS NULL", commentObjectError("42809", `"comment_kind_table" is not an index`)},
		{"COMMENT ON TRIGGER nosuchtrg ON comment_kind_table IS NULL", commentObjectError("42704", `trigger "nosuchtrg" for table "comment_kind_table" does not exist`)},
		{"COMMENT ON TRIGGER comment_kind_trg ON nosuchtbl IS NULL", commentObjectError("42P01", `relation "nosuchtbl" does not exist`)},
		{"COMMENT ON FUNCTION nosuchfn(integer) IS NULL", commentObjectError("42883", `function nosuchfn(integer) does not exist`)},
		{"COMMENT ON FUNCTION nosuchfn IS NULL", commentObjectError("42883", `could not find a function named "nosuchfn"`)},
		{"COMMENT ON FUNCTION comment_kind_fn(bpchar, integer) IS NULL", commentObjectError("42883", `function comment_kind_fn(character, integer) does not exist`)},
		{"COMMENT ON PROCEDURE comment_kind_fn(integer) IS NULL", commentObjectError("42809", `comment_kind_fn(integer) is not a procedure`)},
		{"COMMENT ON FUNCTION comment_kind_proc(integer) IS NULL", commentObjectError("42809", `comment_kind_proc(integer) is not a function`)},
		{"COMMENT ON PROCEDURE comment_kind_proc_out(OUT integer) IS NULL", commentObjectError("42883", `procedure comment_kind_proc_out() does not exist`)},
		{"COMMENT ON ROUTINE nosuchrt(integer) IS NULL", commentObjectError("42883", `function nosuchrt(integer) does not exist`)},
		{"COMMENT ON ROUTINE nosuchrt IS NULL", commentObjectError("42883", `could not find a function named "nosuchrt"`)},
		{
			"COMMENT ON FUNCTION comment_kind_over IS NULL",
			sqlstate.Error{
				Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42725",
				Message: `function name "comment_kind_over" is not unique`,
				Hint:    "Specify the argument list to select the function unambiguously.",
			},
		},
	} {
		commentObjectDiagnostic(t, e, op, tc.query, tc.want)
	}

	// Ownership follows resolution; a missing trigger reports before the
	// ownership check.
	enginetest.RoleExecute(t, e, op, "CREATE ROLE comment_kind_nonowner LOGIN")
	enginetest.RoleExecute(t, e, op, "CREATE SCHEMA comment_kind_hidden")
	enginetest.RoleExecute(t, e, op, "CREATE TYPE comment_kind_hidden.hidden_type AS ENUM ('a')")
	enginetest.RoleExecute(t, e, op, "CREATE TYPE comment_kind_hidden.ht AS ENUM ('a')")
	nonowner, nonownerDone := enginetest.RoleOperation(t, e, "comment_kind_nonowner")
	defer nonownerDone()
	for _, tc := range []struct {
		query string
		want  sqlstate.Error
	}{
		{"COMMENT ON TYPE comment_kind_enum IS 'bad'", commentObjectError("42501", "must be owner of type comment_kind_enum")},
		{"COMMENT ON DOMAIN comment_kind_domain IS 'bad'", commentObjectError("42501", "must be owner of type comment_kind_domain")},
		{"COMMENT ON INDEX comment_kind_index IS 'bad'", commentObjectError("42501", "must be owner of index comment_kind_index")},
		{"COMMENT ON INDEX public.comment_kind_index IS 'bad'", commentObjectError("42501", "must be owner of index comment_kind_index")},
		{"COMMENT ON TRIGGER comment_kind_trg ON comment_kind_table IS 'bad'", commentObjectError("42501", "must be owner of relation comment_kind_table")},
		{"COMMENT ON TRIGGER nosuchtrg ON comment_kind_table IS 'bad'", commentObjectError("42704", `trigger "nosuchtrg" for table "comment_kind_table" does not exist`)},
		{"COMMENT ON FUNCTION comment_kind_fn(integer) IS 'bad'", commentObjectError("42501", "must be owner of function comment_kind_fn")},
		{"COMMENT ON PROCEDURE comment_kind_proc(integer) IS 'bad'", commentObjectError("42501", "must be owner of procedure comment_kind_proc")},
		{"COMMENT ON ROUTINE comment_kind_fn(integer) IS 'bad'", commentObjectError("42501", "must be owner of routine comment_kind_fn")},
		{"COMMENT ON ROUTINE comment_kind_proc(integer) IS 'bad'", commentObjectError("42501", "must be owner of routine comment_kind_proc")},
	} {
		commentObjectDiagnostic(t, e, nonowner, tc.query, tc.want)
	}
	// A schema without USAGE: the explicit path denies, the implicit path
	// omits the schema entirely.
	enginetest.RoleExecute(t, e, nonowner, "SET search_path = comment_kind_hidden, public")
	commentObjectDiagnostic(t, e, nonowner, "COMMENT ON TYPE comment_kind_hidden.hidden_type IS NULL", commentObjectError("42501", "permission denied for schema comment_kind_hidden"))
	commentObjectDiagnostic(t, e, nonowner, "COMMENT ON TYPE ht IS NULL", commentObjectError("42704", `type "ht" does not exist`))
}

func TestCommentTriggerLiveness(t *testing.T) {
	e := engine.New()
	op, done := enginetest.RoleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{
		"CREATE TABLE comment_live_table(id integer)",
		"CREATE FUNCTION comment_live_trigger() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$",
		"CREATE TRIGGER comment_live_trg AFTER INSERT ON comment_live_table FOR EACH ROW EXECUTE PROCEDURE comment_live_trigger()",
	} {
		enginetest.RoleExecute(t, e, op, q)
	}
	tableRef := commentObjectRef(t, e, ids.RelationClassID, "comment_live_table", "")
	firstRef, firstOID := commentTriggerRef(t, e, tableRef, "comment_live_trg")
	enginetest.RoleExecute(t, e, op, "COMMENT ON TRIGGER comment_live_trg ON comment_live_table IS 'first'")
	commentWant(t, e, e.CatalogRead(), firstRef, "first")
	if text, present := commentDescription(t, e, op, ids.TriggerClassID, firstOID); !present || text != "first" {
		t.Fatalf("live trigger pg_description: %q %v", text, present)
	}

	// Dropping the trigger leaves the map entry inert: the descriptor keeps
	// the stored key, while both readback paths and pg_description omit it.
	enginetest.RoleExecute(t, e, op, "DROP TRIGGER comment_live_trg ON comment_live_table")
	current := e.CatalogRead()
	if stored := current.MustRelation(tableRef.Address).TriggerComments[firstOID]; stored != "first" {
		t.Fatalf("dropped trigger comment was not retained: %q", stored)
	}
	commentWant(t, e, current, firstRef, "")
	if text, present := commentDescription(t, e, op, ids.TriggerClassID, firstOID); present {
		t.Fatalf("stale trigger pg_description row: %q", text)
	}
	visited := false
	if err := catalog.VisitTriggerComments(current, func(ids.OID, string) error {
		visited = true
		return nil
	}); err != nil || visited {
		t.Fatalf("stale trigger comment visited: %v %v", visited, err)
	}

	// A re-created trigger owns a new OID, inherits no text, and the old map
	// key never reappears in pg_description.
	enginetest.RoleExecute(t, e, op, "CREATE TRIGGER comment_live_trg AFTER INSERT ON comment_live_table FOR EACH ROW EXECUTE PROCEDURE comment_live_trigger()")
	secondRef, secondOID := commentTriggerRef(t, e, tableRef, "comment_live_trg")
	if secondOID == firstOID {
		t.Fatalf("re-created trigger reused OID %d", firstOID)
	}
	commentWant(t, e, e.CatalogRead(), secondRef, "")
	enginetest.RoleExecute(t, e, op, "COMMENT ON TRIGGER comment_live_trg ON comment_live_table IS 'second'")
	commentWant(t, e, e.CatalogRead(), secondRef, "second")
	if text, present := commentDescription(t, e, op, ids.TriggerClassID, secondOID); !present || text != "second" {
		t.Fatalf("re-created trigger pg_description: %q %v", text, present)
	}
	if text, present := commentDescription(t, e, op, ids.TriggerClassID, firstOID); present {
		t.Fatalf("dropped trigger pg_description row resurfaced: %q", text)
	}
	if stored := e.CatalogRead().MustRelation(tableRef.Address).TriggerComments[firstOID]; stored != "first" {
		t.Fatalf("dropped trigger comment changed: %q", stored)
	}

	// CREATE OR REPLACE TRIGGER inherits the live trigger OID, so its comment
	// stays attached.
	enginetest.RoleExecute(t, e, op, "CREATE OR REPLACE TRIGGER comment_live_trg AFTER INSERT ON comment_live_table FOR EACH ROW EXECUTE PROCEDURE comment_live_trigger()")
	replacedRef, replacedOID := commentTriggerRef(t, e, tableRef, "comment_live_trg")
	if replacedOID != secondOID {
		t.Fatalf("replaced trigger OID %d, want %d", replacedOID, secondOID)
	}
	commentWant(t, e, e.CatalogRead(), replacedRef, "second")
}