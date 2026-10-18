package engine

import (
	"bytes"
	"context"
	"database/sql/driver"
	"errors"
	"reflect"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func schemaRef(t *testing.T, e *Engine, name string) ids.ObjectRef {
	t.Helper()
	v := e.CatalogRead()
	a := v.state.names[ids.NameKey{ClassID: ids.NamespaceClassID, Name: name}]
	if a == (ids.ObjectAddress{}) {
		t.Fatal("missing namespace", name)
	}
	return e.domain.Ref(v.Epoch(), a)
}
func schemaFail(t *testing.T, e *Engine, op *Operation, q, code string) {
	t.Helper()
	before := e.CatalogRead()
	storage := e.storage
	_, err := e.Execute(op, statement(t, q), 0, nil)
	state(t, err, code)
	if e.CatalogRead().state != before.state || !reflect.DeepEqual(e.storage, storage) {
		t.Fatal("failure published catalog/storage", q)
	}
}
func TestSchemaLifecycleIdentityAndAtomicBatch(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE SCHEMA s1 CREATE TABLE t(id INT)", "CREATE SCHEMA s2 CREATE TABLE t(id INT)", "INSERT INTO s1.t VALUES(7)", "INSERT INTO s2.t VALUES(9)"} {
		roleExecute(t, e, op, q)
	}
	ns := schemaRef(t, e, "s1")
	table := roleTableRef(t, e, "s1.t")
	store := e.storage[table.Address]
	old := e.CatalogRead()
	roleExecute(t, e, op, "SET search_path TO s1,s2")
	roleExecute(t, e, op, "ALTER SCHEMA s1 RENAME TO renamed")
	if got := schemaRef(t, e, "renamed"); got != ns {
		t.Fatal("rename changed identity", got, ns)
	}
	if got := roleTableRef(t, e, "renamed.t"); got != table || e.storage[got.Address] != store {
		t.Fatal("rename changed table/storage")
	}
	if old.state.descriptors[ns.Address].Name() != "s1" {
		t.Fatal("mutated retained descriptor")
	}
	if got := tuples(roleExecute(t, e, op, "SHOW search_path")); !reflect.DeepEqual(got, [][]driver.Value{{"s1, s2"}}) {
		t.Fatal(got)
	}
	schemaFail(t, e, op, "DROP SCHEMA renamed", "2BP01")
	schemaFail(t, e, op, "DROP SCHEMA renamed,absent CASCADE", "3F000")
	retained := roleExecute(t, e, op, "SELECT id FROM renamed.t")
	roleExecute(t, e, op, "DROP SCHEMA renamed,renamed CASCADE")
	if _, ok := e.storage[table.Address]; ok {
		t.Fatal("orphan storage")
	}
	if got := tuples(retained); !reflect.DeepEqual(got, [][]driver.Value{{int64(7)}}) {
		t.Fatal(got)
	}
	if got := tuples(roleExecute(t, e, op, "SELECT id FROM s2.t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(9)}}) {
		t.Fatal(got)
	}
	roleExecute(t, e, op, "CREATE SCHEMA renamed")
	if schemaRef(t, e, "renamed").Address == ns.Address {
		t.Fatal("reused namespace identity")
	}
	notices := op.DetachNotices()
	if len(notices) != 1 || notices[0].Code != "00000" || notices[0].Message != "drop cascades to table renamed.t" {
		t.Fatal(notices)
	}
	roleExecute(t, e, op, "CREATE SCHEMA IF NOT EXISTS renamed")
	roleExecute(t, e, op, "DROP SCHEMA IF EXISTS absent")
	notices = op.DetachNotices()
	if len(notices) != 2 || notices[0].Code != "42P06" || notices[1].Code != "00000" {
		t.Fatal(notices)
	}
}
func TestSchemaNamesNestedAtomicity(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	roleExecute(t, e, op, "CREATE USER owner")
	beforeRole := op.RoleState()
	roleExecute(t, e, op, `CREATE SCHEMA "dot.schema" AUTHORIZATION owner CREATE TABLE "dot.schema"."t,quote"(id INT) CREATE TABLE u(note TEXT)`)
	if op.RoleState() != beforeRole {
		t.Fatal("nested changed caller")
	}
	ns := schemaRef(t, e, "dot.schema")
	owner, _ := e.Roles().Lookup("owner")
	if e.state.descriptors[ns.Address].(*namespaceDescriptor).owner != owner.Ref {
		t.Fatal("wrong schema owner")
	}
	for _, q := range []string{`"dot.schema"."t,quote"`, `"dot.schema".u`} {
		if e.state.descriptors[roleTableRef(t, e, q).Address].(*relationDescriptor).owner != owner.Ref {
			t.Fatal("wrong nested owner")
		}
	}
	for _, tc := range []struct{ q, code string }{{"CREATE SCHEMA fail CREATE TABLE t(id INT) CREATE TABLE t(id INT)", "42P07"}, {"CREATE SCHEMA fail CREATE TABLE t(id INT) CREATE TABLE u(id unknown_type)", "42704"}, {"CREATE SCHEMA fail CREATE TABLE other.t(id INT)", "42P15"}, {"CREATE SCHEMA pg_bad", "42939"}} {
		schemaFail(t, e, op, tc.q, tc.code)
	}
	for _, q := range []string{"CREATE SCHEMA fail CREATE VIEW v AS SELECT id FROM t", "CREATE SCHEMA IF NOT EXISTS fail CREATE TABLE t(id INT)", "CREATE SCHEMA fail INSERT INTO t VALUES(1)"} {
		ss, err := parser.Parse(q)
		if err == nil || ss != nil {
			t.Fatal("unsupported nested statement", q, ss, err)
		}
	}
	_, err := e.Execute(op, statement(t, "CREATE TABLE absent.t(id INT)"), 0, nil)
	state(t, err, "3F000")
	_, err = e.Describe(op, statement(t, "SELECT id FROM absent.t"))
	state(t, err, "42P01")
}

func TestSchemaIdentifierNormalization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, tc := range []struct{ sql, name string }{{"FOLDED", "folded"}, {`"PG_allowed"`, "PG_allowed"}, {`"quote""schema"`, "quote\"schema"}} {
		roleExecute(t, e, op, "CREATE SCHEMA "+tc.sql+" CREATE TABLE t(id INT)")
		schemaRef(t, e, tc.name)
		roleExecute(t, e, op, "INSERT INTO "+tc.sql+".t VALUES(13)")
		if got := tuples(roleExecute(t, e, op, "SELECT id FROM "+tc.sql+".t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(13)}}) {
			t.Fatal(tc, got)
		}
	}
	long := strings.Repeat("é", 40)
	normalized := strings.Repeat("é", 31)
	roleExecute(t, e, op, `CREATE SCHEMA "`+long+`" CREATE TABLE t(id INT)`)
	ref := schemaRef(t, e, normalized)
	if len(e.state.descriptors[ref.Address].Name()) != 62 {
		t.Fatal("split UTF8 or wrong identifier bound")
	}
	roleExecute(t, e, op, `INSERT INTO "`+normalized+`".t VALUES(17)`)
	roleExecute(t, e, op, `SELECT set_config('search_path','"`+long+`"',false)`)
	if got := tuples(roleExecute(t, e, op, "SELECT current_schema")); !reflect.DeepEqual(got, [][]driver.Value{{normalized}}) {
		t.Fatal(got)
	}
	if got := tuples(roleExecute(t, e, op, "SELECT id FROM t")); !reflect.DeepEqual(got, [][]driver.Value{{int64(17)}}) {
		t.Fatal(got)
	}
	schemaFail(t, e, op, `CREATE SCHEMA "`+long+`x"`, "42P06")
}

type schemaDefaults struct{ path string }

func (d *schemaDefaults) SessionDefault(_ context.Context, definition SettingDefinition) (string, bool, error) {
	if definition.Name == "search_path" {
		return d.path, true, nil
	}
	return "", false, nil
}
func TestSchemaSearchPathSessionDefaultsAndSpecials(t *testing.T) {
	e := New()
	defaults := &schemaDefaults{path: "s1,s2"}
	a := sessionStart(t, e, defaults)
	defaults.path = "s2,s1"
	b := sessionStart(t, e, defaults)
	// Provider values are captured once into each session's typed defaults.
	// Portable SQL path behavior lives in the namespace reference scenarios.
	for _, tc := range []struct {
		session *Session
		want    []string
	}{
		{a, []string{"s1", "s2"}}, {b, []string{"s2", "s1"}},
	} {
		if got := tc.session.settings.defaultTyped["search_path"].path; !reflect.DeepEqual(got, tc.want) {
			t.Fatal("captured provider default", got, tc.want)
		}
		if got := tc.session.settings.typedValue("search_path").path; !reflect.DeepEqual(got, tc.want) {
			t.Fatal("initial typed setting", got, tc.want)
		}
		override, err := e.settingsDefinitions["search_path"].parse("public")
		if err != nil {
			t.Fatal(err)
		}
		e.mu.Lock()
		tc.session.settings.mu.Lock()
		tc.session.mu.Lock()
		tc.session.settings.values["search_path"] = "public"
		tc.session.settings.typed["search_path"] = override
		e.resetAllSettingsLocked(tc.session)
		got := tc.session.settings.typedValue("search_path").path
		tc.session.mu.Unlock()
		tc.session.settings.mu.Unlock()
		e.mu.Unlock()
		if !reflect.DeepEqual(got, tc.want) {
			t.Fatal("reset lost captured provider default", got, tc.want)
		}
	}
	for _, tc := range []struct {
		input string
		want  []string
	}{{`"dot.name", "comma,name", "quote""name"`, []string{"dot.name", "comma,name", "quote\"name"}}, {`"$user", public`, []string{"$user", "public"}}, {`""`, []string{""}}, {`"", s2`, []string{"", "s2"}}, {"", nil}, {`a"b`, []string{`a"b`}}} {
		got, err := parseSearchPath(tc.input)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc, got, err)
		}
	}
	for _, input := range []string{",", "s1,", `"unterminated`, `a b`} {
		_, err := parseSearchPath(input)
		state(t, err, "22023")
	}
}

func TestSchemaAuthorizationAndIndependentOwners(t *testing.T) {
	e := New()
	admin, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE USER owner", "CREATE USER reader", "CREATE SCHEMA owned AUTHORIZATION owner CREATE TABLE t(id INT)", "CREATE SCHEMA usable", "CREATE SCHEMA create_only"} {
		roleExecute(t, e, admin, q)
	}
	owner, _ := e.Roles().Lookup("owner")
	reader, _ := e.Roles().Lookup("reader")
	roleExecute(t, e, admin, "SET SESSION AUTHORIZATION owner")
	roleExecute(t, e, admin, "CREATE TABLE owned.u(id INT)")
	roleExecute(t, e, admin, "INSERT INTO owned.t VALUES(7)")
	roleError(t, e, admin, "CREATE SCHEMA denied", "42501", "permission denied for database stuntgres", "")
	roleError(t, e, admin, "ALTER SCHEMA owned RENAME TO renamed", "42501", "permission denied for database stuntgres", "")
	roleExecute(t, e, admin, "ALTER SCHEMA owned OWNER TO owner")
	roleExecute(t, e, admin, "RESET SESSION AUTHORIZATION")
	bootstrap := e.roles.root.Read(authorization.PrincipalRef{}).LookupRef("postgres")
	for _, tc := range []struct {
		name   string
		rights authorization.Privileges
	}{{"usable", authorization.UsagePrivilege}, {"create_only", authorization.CreatePrivilege}} {
		if err := e.SetObjectACL(context.Background(), schemaRef(t, e, tc.name), authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: bootstrap, Grantee: authorization.ACLGrantee{Role: reader.Ref}, Privileges: tc.rights}}}); err != nil {
			t.Fatal(err)
		}
	}
	roleExecute(t, e, admin, "SET SESSION AUTHORIZATION reader")
	roleExecute(t, e, admin, "CREATE TABLE create_only.t(id INT)")
	roleExecute(t, e, admin, "SET search_path TO owned,usable,public")
	if got := tuples(roleExecute(t, e, admin, "SELECT current_schema")); !reflect.DeepEqual(got, [][]driver.Value{{"usable"}}) {
		t.Fatal(got)
	}
	schemaFail(t, e, admin, "CREATE TABLE no_fallback(id INT)", "42501")
	roleErrorPosition(t, e, admin, "CREATE TABLE no_fallback(id INT)", "42501", "permission denied for schema usable", "", "14")
	roleErrorPosition(t, e, admin, "/* é */ CREATE TABLE usable.denied(id INT)", "42501", "permission denied for schema usable", "", "22")
	roleExecute(t, e, admin, "SET search_path TO ''")
	roleErrorPosition(t, e, admin, "CREATE TABLE no_destination(id INT)", "3F000", "no schema has been selected to create in", "", "14")
	roleErrorPosition(t, e, admin, "/* é */ CREATE TABLE no_destination(id INT)", "3F000", "no schema has been selected to create in", "", "22")
	_, err := e.Describe(admin, statement(t, "SELECT missing FROM owned.t"))
	state(t, err, "42501")
	_, err = e.Describe(admin, statement(t, "INSERT INTO create_only.t VALUES('invalid')"))
	state(t, err, "42501")
	roleExecute(t, e, admin, "RESET SESSION AUTHORIZATION")
	table := roleTableRef(t, e, "owned.t")
	roleExecute(t, e, admin, "ALTER SCHEMA owned OWNER TO reader")
	if e.state.descriptors[table.Address].(*relationDescriptor).owner != owner.Ref {
		t.Fatal("schema transfer changed table owner")
	}
	schemaFail(t, e, admin, "ALTER SCHEMA owned OWNER TO absent", "42704")
	var out bytes.Buffer
	if err = e.Dump(context.Background(), &out); err == nil || out.Len() != 0 {
		t.Fatal("ACL dump wrote output", err, out.String())
	}
	roleExecute(t, e, admin, "SET SESSION AUTHORIZATION reader")
	roleExecute(t, e, admin, "DROP SCHEMA owned CASCADE")
}

func TestSchemaRestoreIndependentOwnersAndAtomicFailure(t *testing.T) {
	e := New()
	run(t, e, "CREATE USER schema_owner")
	run(t, e, "CREATE USER table_owner")
	run(t, e, "CREATE USER later_owner")
	script := `CREATE SCHEMA empty AUTHORIZATION schema_owner; CREATE SCHEMA mixed AUTHORIZATION schema_owner; SET SESSION AUTHORIZATION table_owner; CREATE TABLE mixed.t(id INT NOT NULL,note TEXT,flag BOOL,big BIGINT); INSERT INTO mixed.t VALUES(7,NULL,true,9223372036854775807);`
	script += `CREATE TABLE mixed.u(id INT); INSERT INTO mixed.u VALUES(8); SET SESSION AUTHORIZATION schema_owner; CREATE TABLE mixed.owned(id INT); INSERT INTO mixed.owned VALUES(9); SET SESSION AUTHORIZATION table_owner; CREATE TABLE mixed.more(id INT); INSERT INTO mixed.more VALUES(11); SET SESSION AUTHORIZATION later_owner; CREATE TABLE mixed.v(id INT); INSERT INTO mixed.v VALUES(9);`
	if err := e.Restore(context.Background(), strings.NewReader(script)); err != nil {
		t.Fatal(err)
	}
	ns := schemaRef(t, e, "mixed")
	table := roleTableRef(t, e, "mixed.t")
	owner, _ := e.Roles().Lookup("schema_owner")
	tableOwner, _ := e.Roles().Lookup("table_owner")
	laterOwner, _ := e.Roles().Lookup("later_owner")
	rolesBefore := e.roles
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "SET search_path TO mixed")
	if d := e.state.descriptors[ns.Address].(*namespaceDescriptor); d.owner != owner.Ref || d.acl.Explicit {
		t.Fatal("schema authority/ACL", d)
	}
	if d := e.state.descriptors[table.Address].(*relationDescriptor); d.owner != tableOwner.Ref || d.acl.Explicit {
		t.Fatal("table authority/ACL", d)
	}
	for _, tc := range []struct {
		name  string
		owner authorization.PrincipalRef
	}{{"mixed.u", tableOwner.Ref}, {"mixed.v", laterOwner.Ref}, {"mixed.owned", owner.Ref}, {"mixed.more", tableOwner.Ref}} {
		d := e.state.descriptors[roleTableRef(t, e, tc.name).Address].(*relationDescriptor)
		if d.owner != tc.owner || d.acl.Explicit || len(d.acl.Entries) != 0 || len(d.columnACL) != 0 {
			t.Fatal("owner grouping leaked insertion authority/ACL", d)
		}
	}
	assertMixed := func() {
		t.Helper()
		ns := schemaRef(t, e, "mixed")
		empty := schemaRef(t, e, "empty")
		for _, a := range []ids.ObjectAddress{ns.Address, empty.Address} {
			d := e.state.descriptors[a].(*namespaceDescriptor)
			if d.owner != owner.Ref || d.acl.Explicit || len(d.acl.Entries) != 0 {
				t.Fatal("namespace owner/ACL", d)
			}
		}
		d := e.state.descriptors[roleTableRef(t, e, "mixed.t").Address].(*relationDescriptor)
		if d.owner != tableOwner.Ref || d.acl.Explicit || len(d.acl.Entries) != 0 || len(d.columnACL) != 0 {
			t.Fatal("table owner/ACL", d)
		}
		result := run(t, e, "SELECT * FROM mixed.t")
		names := []string{"id", "note", "flag", "big"}
		types := []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Bool, sqltype.Int8}
		if len(result.Rows.Schema()) != len(names) {
			t.Fatal("mixed metadata width", result.Rows.Schema())
		}
		for i, c := range result.Rows.Schema() {
			if c.Name != names[i] || c.Usage.Type.ID() != types[i] || !c.NullabilityKnown || c.Nullable != (i != 0) {
				t.Fatal("mixed metadata", result.Rows.Schema())
			}
		}
		if got := tuples(result); !reflect.DeepEqual(got, [][]driver.Value{{int64(7), nil, true, int64(9223372036854775807)}}) {
			t.Fatal(got)
		}
	}
	assertMixed()
	held := run(t, e, "SELECT * FROM mixed.t")
	var dump bytes.Buffer
	if err := e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dump.String(), `CREATE SCHEMA "empty" AUTHORIZATION "schema_owner";`) {
		t.Fatal(dump.String())
	}
	before := e.CatalogRead()
	for _, bad := range []string{"CREATE SCHEMA x AUTHORIZATION absent;", "CREATE SCHEMA x AUTHORIZATION schema_owner; SET SESSION AUTHORIZATION table_owner; CREATE TABLE x.t(id INT); INSERT INTO x.t VALUES(17); INSERT INTO missing VALUES(1);", "CREATE SCHEMA x; CREATE TABLE x.t(id INT); CREATE TABLE x.t(id INT);"} {
		if err := e.Restore(context.Background(), strings.NewReader(bad)); err == nil || e.CatalogRead().state != before.state {
			t.Fatal("failed restore changed target", bad, err)
		}
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := e.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fresh := roleTableRef(t, e, "mixed.t")
	if fresh.Epoch == table.Epoch {
		t.Fatal("replacement reused epoch")
	}
	if d := e.state.descriptors[schemaRef(t, e, "mixed").Address].(*namespaceDescriptor); d.owner != owner.Ref || d.acl.Explicit || len(d.acl.Entries) != 0 {
		t.Fatal("roundtrip schema owner/ACL", d)
	}
	for _, tc := range []struct {
		name  string
		owner authorization.PrincipalRef
		value int64
	}{{"mixed.u", tableOwner.Ref, 8}, {"mixed.v", laterOwner.Ref, 9}, {"mixed.owned", owner.Ref, 9}, {"mixed.more", tableOwner.Ref, 11}} {
		d := e.state.descriptors[roleTableRef(t, e, tc.name).Address].(*relationDescriptor)
		if d.owner != tc.owner || d.acl.Explicit || len(d.acl.Entries) != 0 || len(d.columnACL) != 0 {
			t.Fatal("roundtrip owner/ACL", d)
		}
		if got := tuples(run(t, e, "SELECT id FROM "+tc.name)); !reflect.DeepEqual(got, [][]driver.Value{{tc.value}}) {
			t.Fatal(got)
		}
	}
	assertMixed()
	if e.roles != rolesBefore {
		t.Fatal("controls changed captured roles")
	}
	if got := tuples(sessionSQL(t, s, "SHOW search_path")); !reflect.DeepEqual(got, [][]driver.Value{{"mixed"}}) {
		t.Fatal("controls changed path", got)
	}
	if got := tuples(held); !reflect.DeepEqual(got, [][]driver.Value{{int64(7), nil, true, int64(9223372036854775807)}}) {
		t.Fatal(got)
	}
	op, done := roleOperation(t, e, "table_owner")
	defer done()
	schemaFail(t, e, op, "CREATE TABLE mixed.denied(id INT)", "42501")
	_, err := e.Describe(op, statement(t, "SELECT id FROM mixed.t"))
	state(t, err, "42501")
	_, err = e.Describe(op, statement(t, "INSERT INTO mixed.t VALUES(19,NULL,true,1)"))
	state(t, err, "42501")
	schemaFail(t, e, op, "INSERT INTO mixed.t VALUES(19,NULL,true,1)", "42501")
}

func TestSchemaPreparedSignatureBeforeImporter(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, q := range []string{"CREATE SCHEMA s1 CREATE TABLE t(id INT)", "CREATE SCHEMA s2 CREATE TABLE t(id TEXT)", "SET search_path TO s1"} {
		roleExecute(t, e, op, q)
	}
	// Parameters retain their source types across rebinding. A changed result
	// signature, however, must be rejected before the importer is called.
	ast := statement(t, "SELECT $1::integer,id FROM t")
	expected, err := e.Describe(op, ast)
	if err != nil {
		t.Fatal(err)
	}
	roleExecute(t, e, op, "SET search_path TO s2")
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	scope.Expected = &expected
	called := false
	_, err = scope.Execute(ast, 1, func(context.Context, ParameterInfo, []sqltype.Value, sqltype.InputEnvironment) error {
		called = true
		return nil
	})
	if finish := scope.Finish(err); finish == nil || called {
		t.Fatal("signature imported or succeeded", finish, called)
	}
	var diagnostic *sqlstate.Error
	want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: "cached plan must not change result type"}
	if !errors.As(err, &diagnostic) || *diagnostic != want {
		t.Fatalf("diagnostic %#v (%v) want %#v", diagnostic, err, want)
	}
	if got := tuples(roleExecute(t, e, op, "SELECT * FROM s2.t")); len(got) != 0 {
		t.Fatal(got)
	}
}
