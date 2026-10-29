package reference_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func connectbyConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for v := range fixture.ConnectbyDefs {
		args := strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(fixture.ConnectbyArgs[v], "$1::text", "'row2'"), "$2::integer", "0"), "$3::text", "'~'")
		q := fixture.ConnectbyQuery(v, args)
		if v == 3 {
			q = strings.Replace(q, "node text", `"Node Name" text`, 1)
		}
		fixture.ConnectbyRun(t, p, b, c, fixture.Exec(fmt.Sprintf("CREATE VIEW cb_control_%d AS %s", v, q), 0))
	}
	fixture.ConnectbyRun(t, p, b, c, fixture.Exec("CREATE VIEW cb_control_nested AS SELECT * FROM cb_control_3", 0))
}
func connectbyControlRead(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for v := range fixture.ConnectbyDefs {
		q := fmt.Sprintf("SELECT * FROM cb_control_%d", v)
		if v >= 2 {
			q += " ORDER BY serial"
		}
		s := fixture.ConnectbySelect(v, q, fixture.ConnectbyLayout(v, fixture.ConnectbyTreeRows))
		if v == 3 {
			s.Columns[0] = "Node Name"
		}
		fixture.ConnectbyRun(t, p, b, c, s)
	}
	s := fixture.ConnectbySelect(3, "SELECT * FROM cb_control_nested ORDER BY serial", fixture.ConnectbyTreeRows)
	s.Columns[0] = "Node Name"
	fixture.ConnectbyRun(t, p, b, c, s)
}
func connectbyNativeControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, held fixture.HeldStatement) {
	t.Helper()
	// SQL consumers above run in both modes; native controls have no PG analogue.
	if b.PG {
		return
	}
	q := "SELECT * FROM cb_control_nested ORDER BY serial"
	rows, err := c.QueryContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	dump := fixture.ExtensionDump(t, p.Ctx, p.Native)
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(columns, []string{"Node Name", "parent", "level", "branch", "serial"}) {
		t.Fatal("retained names", columns)
	}
	for i, typ := range []string{"TEXT", "TEXT", "INT4", "TEXT", "INT4"} {
		if len(types) != 5 || types[i].DatabaseTypeName() != typ {
			t.Fatal("retained types", types)
		}
	}
	for _, want := range fixture.ConnectbyTreeRows {
		if !rows.Next() {
			t.Fatal("lost finalized rows", rows.Err())
		}
		got := make([]any, 5)
		ptrs := make([]any, 5)
		for i := range got {
			ptrs[i] = &got[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i := range got {
			got[i] = fixture.Normalize(got[i])
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("retained row", got, want)
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("retained tail", rows.Err())
	}
	rows.Close()
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	connectbyControlRead(t, p, b, c)
	fixture.ConnectbyRun(t, p, b, held, fixture.ConnectbySelect(3, held.SQL, fixture.ConnectbyTreeRows, "row2", 0, "~"))
	before := fixture.ExtensionDump(t, p.Ctx, p.Native)
	for _, bad := range []string{dump + "\nINVALID SQL;", strings.Replace(dump, "VERSION '1.0'", "VERSION 'missing'", 1), strings.Replace(dump, "stuntgres-extension-owners-v3", "stuntgres-extension-owners-v999", 1), connectbyBadOwnerDump(t, dump)} {
		if bad == dump {
			t.Fatal("malformed restore fixture unchanged")
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)); err == nil {
			t.Fatal("invalid restore accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("failed restore mutated catalog")
		}
		connectbyControlRead(t, p, b, c)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	err = stuntgres.Restore(ctx, p.Native, &fixture.CatalogControlCancelReader{Cancel: cancel})
	cancel()
	if !errors.Is(err, context.Canceled) || fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
		t.Fatal("canceled restore atomicity", err)
	}
	connectbyControlRead(t, p, b, c)
	other, target, record := pgcryptoControlDestination(t, p, "connectby_destination")
	if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(dump)); err != nil {
		t.Fatal(err)
	}
	destination := b
	destination.DB = other
	destination.Recorder = record
	connectbyControlRead(t, p, destination, target)
	fixture.ConnectbyRun(t, p, destination, target, fixture.ConnectbySelect(3, held.SQL, fixture.ConnectbyTreeRows, "row2", 0, "~"))
	fixture.ConnectbyRun(t, p, destination, target, fixture.Exec("INSERT INTO cb_tree VALUES('new','row8',1)", 1))
	updated := append(append([][]any{}, fixture.ConnectbyTreeRows...), []any{"new", "row8", int64(4), "row2~row4~row6~row8~new", int64(7)})
	fixture.ConnectbyRun(t, p, destination, target, fixture.ConnectbySelect(3, held.SQL, updated, "row2", 0, "~"))
	fixture.ConnectbyRun(t, p, b, held, fixture.ConnectbySelect(3, held.SQL, fixture.ConnectbyTreeRows, "row2", 0, "~"))
	// Retained demand must bind the replacement installation after drop/recreate.
	fixture.ConnectbyRun(t, p, b, c, fixture.Exec("DROP EXTENSION tablefunc CASCADE", 0))
	fixture.ConnectbyRun(t, p, b, c, fixture.Exec("CREATE EXTENSION tablefunc SCHEMA cb_part", 0))
	fixture.ConnectbyRun(t, p, b, held, fixture.ConnectbySelect(3, held.SQL, fixture.ConnectbyTreeRows, "row2", 0, "~"))
}
func TestReferenceConnectbyControls(t *testing.T) {
	connectbyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.ConnectbyTree(t, p, b, c)
		connectbyConsumers(t, p, b, c)
		connectbyControlRead(t, p, b, c)
		q := fixture.ConnectbyQuery(3, fixture.ConnectbyArgs[3])
		held := connectbyHold(t, p, c, q)
		fixture.ConnectbyRun(t, p, b, held, fixture.ConnectbySelect(3, q, fixture.ConnectbyTreeRows, "row2", 0, "~"))
		connectbyNativeControls(t, p, b, c, held)
	})
}
func TestReferenceConnectbySmoke(t *testing.T) {
	connectbyCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.ConnectbyTree(t, p, b, c)
		q := fixture.ConnectbyQuery(3, fixture.ConnectbyArgs[3])
		held := connectbyHold(t, p, c, q)
		s := fixture.ConnectbySelect(3, q, fixture.ConnectbyTreeRows, "row2", 0, "~")
		fixture.ConnectbyRun(t, p, b, c, s)
		fixture.ConnectbyRun(t, p, b, held, s)
		fixture.ConnectbyRun(t, p, b, held, fixture.ConnectbySelect(3, q, nil, nil, 0, "~"))
		fixture.ConnectbyRun(t, p, b, held, fixture.ConnectbySelect(3, q, [][]any{{"row2", nil, int64(0), "row2", int64(1)}, {"row5", "row2", int64(1), "row2~row5", int64(2)}, {"row4", "row2", int64(1), "row2~row4", int64(3)}}, "row2", 1, "~"))
		fixture.ConnectbyRun(t, p, b, c, fixture.Exec("INSERT INTO cb_tree VALUES('cycle_b','cycle_a',1),('cycle_a','cycle_b',1)", 2))
		failure := connectbyError(q, "42P19", "infinite recursion detected", "")
		failure.Args = []any{"cycle_a", 0, "~"}
		fixture.ConnectbyRun(t, p, b, held, failure)
		fixture.ConnectbyRun(t, p, b, held, fixture.ConnectbySelect(3, q, [][]any{{"cycle_a", nil, int64(0), "cycle_a", int64(1)}, {"cycle_b", "cycle_a", int64(1), "cycle_a~cycle_b", int64(2)}}, "cycle_a", 1, "~"))
		fixture.ConnectbyRun(t, p, b, c, fixture.Exec("CREATE TABLE cb_smoke_atomic(v integer)", 0))
		fixture.ConnectbyRun(t, p, b, c, fixture.Exec("INSERT INTO cb_smoke_atomic VALUES(99)", 1))
		atomic := connectbyError("INSERT INTO cb_smoke_atomic SELECT level FROM cb_part.connectby('cb_tree','k','p','cycle_a',0) AS t(node text,parent text,level integer)", "42P19", "infinite recursion detected", "")
		atomic.SelectRows = false
		fixture.ConnectbyRun(t, p, b, c, atomic)
		fixture.ConnectbyRun(t, p, b, c, fixture.Select("SELECT v FROM cb_smoke_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(99)}}))
		fixture.ConnectbyRun(t, p, b, c, fixture.Exec("INSERT INTO cb_smoke_atomic SELECT level FROM cb_part.connectby('cb_tree','k','p','row2',1) AS t(node text,parent text,level integer)", 3))
		fixture.ConnectbyRun(t, p, b, c, fixture.Select("SELECT v FROM cb_smoke_atomic ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(1)}, {int64(99)}}))
		fixture.ConnectbyRun(t, p, b, c, fixture.Exec(`CREATE TABLE "CB Tree"("Key" text,"Parent" text,"Order" integer)`, 0))
		fixture.ConnectbyRun(t, p, b, c, fixture.Exec(`INSERT INTO "CB Tree" VALUES('child','root',1)`, 1))
		quoted := fixture.ConnectbyQuery(3, `'"CB Tree"','"Key"','"Parent"','"Order"','root',0,'~'`)
		fixture.ConnectbyRun(t, p, b, c, fixture.ConnectbySelect(3, quoted, [][]any{{"root", nil, int64(0), "root", int64(1)}, {"child", "root", int64(1), "root~child", int64(2)}}))
		connectbyConsumers(t, p, b, c)
		connectbyControlRead(t, p, b, c)
		connectbyNativeControls(t, p, b, c, held)
	})
}

func connectbyBadOwnerDump(t *testing.T, dump string) string {
	t.Helper()
	const marker = "-- stuntgres-extension-owners-v3 "
	for _, line := range strings.Split(dump, "\n") {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(line, marker))
		if err != nil {
			t.Fatal(err)
		}
		var record map[string]json.RawMessage
		if err := json.Unmarshal(payload, &record); err != nil {
			t.Fatal(err)
		}
		var name string
		if err := json.Unmarshal(record["name"], &name); err != nil {
			t.Fatal(err)
		}
		if name != "tablefunc" {
			continue
		}
		var members []map[string]json.RawMessage
		if err := json.Unmarshal(record["members"], &members); err != nil {
			t.Fatal(err)
		}
		if len(members) != 16 {
			t.Fatal("selected tablefunc owner inventory", len(members))
		}
		selected := -1
		for i, member := range members {
			var kind, schema, name, signature, version string
			for key, target := range map[string]*string{"kind": &kind, "schema": &schema, "name": &name, "signature": &signature, "version": &version} {
				if err := json.Unmarshal(member[key], target); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "procedure" && schema == "cb_part" && name == "connectby" && signature == "pg_catalog.text, pg_catalog.text, pg_catalog.text, pg_catalog.text, integer, pg_catalog.text" && version == "1.0" && member["declaration"] != nil {
				if selected != -1 {
					t.Fatal("duplicate symbolic connectby declaration")
				}
				selected = i
			}
		}
		if selected == -1 {
			t.Fatal("missing symbolic connectby declaration")
		}
		// Keep every other field, including ACL and immutable origin, intact.
		members[selected]["owner"] = json.RawMessage(`"cb_absent_owner"`)
		record["members"], err = json.Marshal(members)
		if err != nil {
			t.Fatal(err)
		}
		payload, err = json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		return strings.Replace(dump, line, marker+base64.StdEncoding.EncodeToString(payload), 1)
	}
	t.Fatal("missing tablefunc owner record")
	return ""
}
