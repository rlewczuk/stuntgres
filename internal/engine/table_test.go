package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"stuntgres/internal/sqlstate"
	"testing"
)

func TestTableCoreShapeAndAtomicDrop(t *testing.T) {
	e := New()
	run(t, e, "CREATE TABLE one(a INT NOT NULL)")
	run(t, e, "CREATE TABLE two(b TEXT)")
	run(t, e, "INSERT INTO one VALUES(7)")
	run(t, e, "CREATE TABLE clone(head BOOL,LIKE one,middle BIGINT,LIKE two,tail TEXT)")
	clone := run(t, e, "SELECT * FROM clone")
	if clone.Count != 0 || len(clone.Rows.Schema()) != 5 {
		t.Fatal(clone)
	}
	ref := roleTableRef(t, e, "clone")
	for i, want := range []struct {
		name, typ string
		nullable  bool
	}{{"head", "BOOL", true}, {"a", "INT4", false}, {"middle", "INT8", true}, {"b", "TEXT", true}, {"tail", "TEXT", true}} {
		c := clone.Rows.Schema()[i]
		if c.Name != want.name || c.Usage.Type.DatabaseName() != want.typ || !c.NullabilityKnown || c.Nullable != want.nullable || c.OriginTable != uint32(ref.Address.ObjectID) || c.OriginAttribute != int16(i+1) {
			t.Fatal(c, want)
		}
	}
	clone.Rows.Close()
	for _, q := range []string{"CREATE TABLE bad(LIKE one,a TEXT)", "CREATE TABLE bad(LIKE one,LIKE one)"} {
		_, err := testExecute(e, context.Background(), statement(t, q), nil)
		state(t, err, "42701")
	}
	run(t, e, "CREATE TABLE z()")
	run(t, e, "CREATE TABLE zl(LIKE z)")
	for _, name := range []string{"z", "zl"} {
		r := run(t, e, "SELECT * FROM "+name)
		if len(r.Rows.Schema()) != 0 || r.Count != 0 {
			t.Fatal(r)
		}
		r.Rows.Close()
	}
	run(t, e, "DROP TABLE one RESTRICT")
	run(t, e, "INSERT INTO clone VALUES(TRUE,9,10,'x','y')")
	if got := tuples(run(t, e, "SELECT * FROM clone")); !reflect.DeepEqual(got, [][]driver.Value{{true, int64(9), int64(10), "x", "y"}}) {
		t.Fatal(got)
	}
	for _, mode := range []string{"", " RESTRICT", " CASCADE"} {
		run(t, e, "CREATE TABLE a(id INT)")
		run(t, e, "CREATE TABLE b(id INT)")
		run(t, e, "INSERT INTO a VALUES(11)")
		run(t, e, "INSERT INTO b VALUES(22)")
		_, err := testExecute(e, context.Background(), statement(t, "DROP TABLE a,missing,b"+mode), nil)
		state(t, err, "42P01")
		for i, n := range []string{"a", "b"} {
			if got := tuples(run(t, e, "SELECT * FROM "+n)); !reflect.DeepEqual(got, [][]driver.Value{{int64(11 + 11*i)}}) {
				t.Fatal(got)
			}
		}
		run(t, e, "DROP TABLE IF EXISTS a,missing,public.a,b"+mode)
		for _, n := range []string{"a", "b"} {
			_, err = testExecute(e, context.Background(), statement(t, "SELECT * FROM "+n), nil)
			state(t, err, "42P01")
		}
	}
}
func TestTableCoreFullDiagnosticsAndSkip(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE TABLE t(id INT)")
	roleExecute(t, e, op, "INSERT INTO t VALUES(11)")
	ref := roleTableRef(t, e, "t")
	d, _ := e.CatalogRead().Descriptor(ref.Address)
	for _, q := range []string{"CREATE TABLE IF NOT EXISTS t(other TEXT)", "CREATE TABLE IF NOT EXISTS t(id INT,id TEXT)", "CREATE TABLE IF NOT EXISTS t(id no_such_type)", "CREATE TABLE IF NOT EXISTS t(id SMALLINT)", "CREATE TABLE IF NOT EXISTS t(id NAME)", "CREATE TABLE IF NOT EXISTS t(LIKE missing)", "CREATE TABLE IF NOT EXISTS t(LIKE missing INCLUDING ALL)"} {
		roleExecute(t, e, op, q)
		notices := op.DetachNotices()
		want := sqlstate.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "t" already exists, skipping`}
		if !reflect.DeepEqual(notices, []sqlstate.Error{want}) {
			t.Fatal(notices)
		}
		now, _ := e.CatalogRead().Descriptor(ref.Address)
		if now != d || roleTableRef(t, e, "t") != ref {
			t.Fatal("skip changed identity")
		}
	}
	for _, tc := range []struct{ sql, code, message, position string }{
		{"CREATE TABLE t(id INT)", "42P07", `relation "t" already exists`, ""},
		{"CREATE TABLE bad(id INT,id TEXT)", "42701", `column "id" specified more than once`, ""},
		{"CREATE TABLE bad (id no_such_type)", "42704", `type "no_such_type" does not exist`, "22"},
		{"/*é*/ CREATE TABLE bad (id no_such_type)", "42704", `type "no_such_type" does not exist`, "28"},
		{"CREATE TABLE clone (LIKE missing)", "42P01", `relation "missing" does not exist`, "26"},
		{"/*é*/ CREATE TABLE clone (LIKE missing)", "42P01", `relation "missing" does not exist`, "32"},
		{"CREATE TABLE sys(ctid INT)", "42701", `column name "ctid" conflicts with a system column name`, ""},
		{"DROP TABLE public.missing", "42P01", `table "missing" does not exist`, ""},
		{"DROP TABLE absent_schema.t", "3F000", `schema "absent_schema" does not exist`, ""},
	} {
		if _, err := e.Describe(op, statement(t, tc.sql)); err != nil {
			t.Fatal("utility preparation failed", err)
		}
		_, err := e.Execute(op, statement(t, tc.sql), 0, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code, Message: tc.message, Position: tc.position}
		if !errors.As(err, &got) || *got != want {
			t.Fatalf("%s: %#v (%v), want %#v", tc.sql, got, err, want)
		}
	}
	for _, prefix := range []string{"INCLUDING", "EXCLUDING"} {
		for _, option := range []string{"COMMENTS", "COMPRESSION", "CONSTRAINTS", "DEFAULTS", "GENERATED", "IDENTITY", "INDEXES", "STATISTICS", "STORAGE", "ALL"} {
			q := fmt.Sprintf("CREATE TABLE bad(LIKE t %s %s)", prefix, option)
			_, err := e.Execute(op, statement(t, q), 0, nil)
			state(t, err, "0A000")
			_, err = e.Execute(op, statement(t, "SELECT * FROM bad"), 0, nil)
			state(t, err, "42P01")
		}
	}
	roleExecute(t, e, op, "DROP TABLE IF EXISTS missing,absent_schema.t")
	wantNotices := []sqlstate.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `table "missing" does not exist, skipping`}, {Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `schema "absent_schema" does not exist, skipping`}}
	if got := op.DetachNotices(); !reflect.DeepEqual(got, wantNotices) {
		t.Fatal(got)
	}
	if got := tuples(roleExecute(t, e, op, "SELECT * FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(11)}}) {
		t.Fatal(got)
	}
}
func TestTableCoreAuthorizationAndRealRoleCleanup(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER owner")
	roleExecute(t, e, op, "CREATE USER reader")
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION owner")
	roleExecute(t, e, op, "CREATE TABLE source(a INT NOT NULL)")
	roleExecute(t, e, op, "CREATE TABLE owned(id INT)")
	source := roleTableRef(t, e, "source")
	reader, _ := e.Roles().Lookup("reader")
	owner := op.RoleState().CurrentUser
	entry := ACLEntry{Grantor: owner, Grantee: ACLGrantee{Role: reader.Ref}, Privileges: SelectPrivilege}
	column := source
	column.Address.SubID = 1
	if err := e.SetObjectACL(context.Background(), column, ACLValue{Explicit: true, Entries: []ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION reader")
	roleError(t, e, op, "CREATE TABLE denied(LIKE source)", "42501", "permission denied for table source", "")
	roleError(t, e, op, "DROP TABLE owned,source", "42501", "must be owner of table owned", "")
	if err := e.SetObjectACL(context.Background(), source, ACLValue{Explicit: true, Entries: []ACLEntry{entry}}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "CREATE TABLE clone(LIKE source)")
	clone := e.CatalogRead().state.descriptors[roleTableRef(t, e, "clone").Address].(*relationDescriptor)
	if clone.owner != reader.Ref || clone.acl.Explicit || len(clone.columnACL) != 0 {
		t.Fatal("LIKE copied ACL/owner")
	}
	roleError(t, e, op, "DROP TABLE clone,source CASCADE", "42501", "must be owner of table source", "")
	if _, ok := e.CatalogRead().Descriptor(clone.address); !ok {
		t.Fatal("later denial dropped authorized earlier target")
	}
	if roleTableRef(t, e, "source") != source {
		t.Fatal("later denial changed source")
	}
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	_, err := e.Execute(op, statement(t, "DROP ROLE owner"), 0, nil)
	state(t, err, "2BP01")
	roleExecute(t, e, op, "DROP TABLE source,owned CASCADE")
	roleExecute(t, e, op, "DROP ROLE owner")
	_, err = e.Execute(op, statement(t, "DROP ROLE reader"), 0, nil)
	state(t, err, "2BP01")
	roleExecute(t, e, op, "DROP TABLE clone")
	roleExecute(t, e, op, "DROP ROLE reader")
}

func TestTableCoreSchemaOwnerAndACLBlockerCleanup(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER schema_owner")
	roleExecute(t, e, op, "CREATE USER acl_reader")
	roleExecute(t, e, op, "CREATE TABLE secured(id INT)")
	reader, _ := e.Roles().Lookup("acl_reader")
	ref := roleTableRef(t, e, "secured")
	entry := ACLEntry{Grantor: op.RoleState().CurrentUser, Grantee: ACLGrantee{Role: reader.Ref}, Privileges: SelectPrivilege}
	acl := ACLValue{Explicit: true, Entries: []ACLEntry{entry}}
	if err := e.SetObjectACL(context.Background(), ref, acl); err != nil {
		t.Fatal(err)
	}
	column := ref
	column.Address.SubID = 1
	if err := e.SetObjectACL(context.Background(), column, acl); err != nil {
		t.Fatal(err)
	}
	_, err := e.Execute(op, statement(t, "DROP ROLE acl_reader"), 0, nil)
	state(t, err, "2BP01")
	owner, _ := e.Roles().Lookup("schema_owner")
	replacement := cloneCatalog(e.CatalogRead().state)
	nsaddr := ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	ns := *replacement.descriptors[nsaddr].(*namespaceDescriptor)
	ns.owner = owner.Ref
	replacement.descriptors[nsaddr] = &ns
	if _, err = e.ReplaceCatalog(context.Background(), CatalogRead{e, replacement}); err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION schema_owner")
	// Neither relation ownership nor ordinary ACL grants DROP; schema ownership does.
	roleExecute(t, e, op, "DROP TABLE secured RESTRICT")
	roleExecute(t, e, op, "RESET SESSION AUTHORIZATION")
	roleExecute(t, e, op, "DROP ROLE acl_reader")
	if _, ok := e.Roles().Lookup("schema_owner"); !ok {
		t.Fatal("DROP removed schema owner")
	}
	if _, ok := e.CatalogRead().Descriptor(nsaddr); !ok {
		t.Fatal("DROP removed namespace")
	}
}

func TestTableCoreKnownUnsupportedTypesExecute(t *testing.T) {
	e := New()
	for _, name := range []string{"TIMESTAMPTZ", "JSONB"} {
		q := "CREATE TABLE bad (id " + name + ")"
		stmt := statement(t, q)
		if _, err := testDescribe(e, stmt); err != nil {
			t.Fatal("semantic type rejection during preparation", name, err)
		}
		_, err := testExecute(e, context.Background(), stmt, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "unsupported column type", Position: "22"}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(name, got, err, want)
		}
		_, err = testExecute(e, context.Background(), statement(t, "SELECT * FROM bad"), nil)
		state(t, err, "42P01")
	}
	run(t, e, `CREATE TABLE supported_uuid(v "uuid")`)
	run(t, e, `INSERT INTO supported_uuid VALUES('{A0EEBC999C0B4EF8BB6D6BB9BD380A11}')`)
}

func TestTableCoreQuotedTypeNamesResolveRawCatalogNames(t *testing.T) {
	e := New()
	run(t, e, `CREATE TABLE raw(i "int4" NOT NULL,b "int8",s "text",f "bool",n "int2")`)
	result := run(t, e, "SELECT * FROM raw")
	for i, want := range []string{"INT4", "INT8", "TEXT", "BOOL", "INT2"} {
		if result.Rows.Schema()[i].Usage.Type.DatabaseName() != want {
			t.Fatal(result.Rows.Schema()[i])
		}
	}
	result.Rows.Close()
	for _, name := range []string{"integer", "int", "bigint", "boolean", "smallint", "real", "decimal", "INTEGER", "INT4", "SERIAL"} {
		stmt := statement(t, `CREATE TABLE bad (id "`+name+`")`)
		if _, err := testDescribe(e, stmt); err != nil {
			t.Fatal("quoted unknown failed during preparation", err)
		}
		_, err := testExecute(e, context.Background(), stmt, nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: `type "` + name + `" does not exist`, Position: "22"}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(name, got, err, want)
		}
	}
	for _, name := range []string{"serial", "smallserial", "bigserial"} {
		_, err := testExecute(e, context.Background(), statement(t, `CREATE TABLE bad (id "`+name+`")`), nil)
		state(t, err, "0A000")
	}
	run(t, e, `CREATE TABLE IF NOT EXISTS raw(id "integer")`)
	_, err := testExecute(e, context.Background(), statement(t, "SELECT * FROM bad"), nil)
	state(t, err, "42P01")
}

func TestTableCoreQuotedTypeDiagnosticSpelling(t *testing.T) {
	e := New()
	for _, tc := range []struct{ q, message string }{{`CREATE TABLE bad (id "a""b")`, `type "a"b" does not exist`}, {`CREATE TABLE bad (id "a\b")`, `type "a\b" does not exist`}} {
		_, err := testExecute(e, context.Background(), statement(t, tc.q), nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: tc.message, Position: "22"}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(got, err, want)
		}
	}
}

func TestTableCoreSystemColumnNamesAndOid(t *testing.T) {
	e := New()
	for _, name := range []string{"ctid", "xmin", "cmin", "xmax", "cmax", "tableoid"} {
		_, err := testExecute(e, context.Background(), statement(t, "CREATE TABLE bad("+name+" INT)"), nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42701", Message: `column name "` + name + `" conflicts with a system column name`}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(name, got, err, want)
		}
	}
	run(t, e, `CREATE TABLE ordinary(oid INT,"CTID" TEXT)`)
	result := run(t, e, "SELECT * FROM ordinary")
	if len(result.Rows.Schema()) != 2 || result.Rows.Schema()[0].Name != "oid" || result.Rows.Schema()[1].Name != "CTID" {
		t.Fatal(result)
	}
	result.Rows.Close()
}

func TestTableCoreQuotedLikeSourceDiagnostics(t *testing.T) {
	e := New()
	for _, tc := range []struct{ q, message string }{{`CREATE TABLE clone (LIKE "a""b")`, `relation "a"b" does not exist`}, {`CREATE TABLE clone (LIKE "a\b")`, `relation "a\b" does not exist`}} {
		_, err := testExecute(e, context.Background(), statement(t, tc.q), nil)
		var got *sqlstate.Error
		want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P01", Message: tc.message, Position: "26"}
		if !errors.As(err, &got) || *got != want {
			t.Fatal(got, err, want)
		}
	}
}

func TestTableCoreQuotedDuplicateRelationPublicationConflict(t *testing.T) {
	for _, tc := range []struct{ identifier, message string }{
		{`"a""b"`, `relation "a"b" already exists`},
		{`"a\b"`, `relation "a\b" already exists`},
	} {
		t.Run(tc.identifier, func(t *testing.T) {
			e := New()
			var winner CatalogRead
			var winnerRef ObjectRef
			var loser ObjectAddress
			e.providers[RelationClassID] = &publicationConflictProvider{
				CatalogProvider: e.providers[RelationClassID],
				publishWinner: func(d Descriptor) {
					loser = d.Address()
					run(t, e, "CREATE TABLE "+tc.identifier+"(id INT NOT NULL)")
					run(t, e, "INSERT INTO "+tc.identifier+" VALUES(11)")
					winner = e.CatalogRead()
					winnerRef = roleTableRef(t, e, tc.identifier)
				},
			}
			result, err := testExecute(e, context.Background(), statement(t, "CREATE TABLE "+tc.identifier+"(lost TEXT)"), nil)
			var got *sqlstate.Error
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P07", Message: tc.message}
			if !errors.As(err, &got) || *got != want {
				t.Errorf("conflict diagnostic %#v (%v), want %#v", got, err, want)
			}
			if result.Count != 0 || result.Rows != nil {
				t.Fatal("loser exposed result", result)
			}
			after := e.CatalogRead()
			if winner.state == nil || after.state != winner.state || roleTableRef(t, e, tc.identifier) != winnerRef {
				t.Fatal("winner identity changed")
			}
			if _, present := after.Descriptor(loser); present || e.storage[loser] != nil {
				t.Fatal("loser published descriptor/storage")
			}
			beforeDescriptor, _ := winner.Descriptor(winnerRef.Address)
			afterDescriptor, _ := after.Descriptor(winnerRef.Address)
			if beforeDescriptor != afterDescriptor {
				t.Fatal("winner descriptor changed")
			}
			rows := run(t, e, "SELECT * FROM "+tc.identifier)
			if len(rows.Rows.Schema()) != 1 || rows.Rows.Schema()[0].Name != "id" || rows.Rows.Schema()[0].Usage.Type.DatabaseName() != "INT4" || !rows.Rows.Schema()[0].NullabilityKnown || rows.Rows.Schema()[0].Nullable {
				t.Fatal("winner shape changed", rows.Rows.Schema())
			}
			if got := tuples(rows); !reflect.DeepEqual(got, [][]driver.Value{{int64(11)}}) {
				t.Fatal("winner data changed", got)
			}
		})
	}
}
