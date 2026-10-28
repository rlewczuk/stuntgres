package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

var triggerModeCases = []struct{ action, code string }{
	{"ENABLE TRIGGER", "O"}, {"DISABLE TRIGGER", "D"},
	{"ENABLE REPLICA TRIGGER", "R"}, {"ENABLE ALWAYS TRIGGER", "A"},
}

func triggerModeActive(mode, role string) bool {
	return mode == "A" || mode == "O" && role != "replica" || mode == "R" && role == "replica"
}

func triggerModeSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE tm_data(id integer PRIMARY KEY,v integer)",
		"CREATE FUNCTION tm_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+100; RETURN NEW; END$$",
		"CREATE TRIGGER a BEFORE INSERT ON tm_data FOR EACH ROW EXECUTE FUNCTION tm_add()")
}

// Every pg_trigger definition field except the deliberately mutable mode.
// Raw catalog carriers retain WHEN/arguments/attribute provenance without
// depending on the separately scoped pg_get_triggerdef deparser.
const triggerModeDefinitionColumns = "oid,tgrelid,tgparentid,tgname,tgfoid,tgtype,tgisinternal,tgconstrrelid,tgconstrindid,tgconstraint,tgdeferrable,tginitdeferred,tgnargs,tgattr,tgargs,tgqual,tgoldtable,tgnewtable"

func triggerModeDefinitions(t *testing.T, p *fixture.Pair, c *sql.Conn, table string) [][]any {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT "+triggerModeDefinitionColumns+" FROM pg_trigger WHERE tgrelid=$1::regclass ORDER BY tgname", table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var result [][]any
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		for i, value := range values {
			if data, ok := value.([]byte); ok {
				values[i] = string(data)
			}
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func triggerModeCatalog(table string, rows ...[]any) fixture.Step {
	return fixture.Select("SELECT tgname,tgenabled FROM pg_trigger WHERE tgrelid=$1::regclass ORDER BY tgname", []string{"tgname", "tgenabled"}, []string{"NAME", "CHAR"}, rows, table)
}

func triggerModeCount(table string, n int64) fixture.Step {
	return fixture.Select("SELECT count(*) AS n FROM "+table, []string{"n"}, []string{"INT8"}, [][]any{{n}})
}

func triggerModeQuote(name string) string { return `"` + strings.ReplaceAll(name, `"`, `""`) + `"` }
func triggerModeNumber(n int64) string    { return strconv.FormatInt(n, 10) }

// Resolve event slots from documented pg_trigger event bits, never allocator order.
func triggerModeFKNames(t *testing.T, p *fixture.Pair, c *sql.Conn, constraint string) [4]string {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, "SELECT t.tgname,t.tgtype,t.tgrelid=f.conrelid AS child FROM pg_trigger t JOIN pg_constraint f ON f.oid=t.tgconstraint WHERE f.conname=$1::name ORDER BY t.oid", constraint)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var names [4]string
	count := 0
	for rows.Next() {
		var name string
		var kind int64
		var child bool
		if err := rows.Scan(&name, &kind, &child); err != nil {
			t.Fatal(err)
		}
		slot := -1
		switch kind {
		case 9:
			slot = 0
		case 5:
			slot = 2
		case 17:
			if child {
				slot = 3
			} else {
				slot = 1
			}
		}
		if slot < 0 || names[slot] != "" || name == "" {
			t.Fatal("unexpected FK event identity", name, kind, child)
		}
		names[slot] = name
		count++
	}
	if err := rows.Err(); err != nil || count != 4 {
		t.Fatal("four FK slots", names, err)
	}
	return names
}

func triggerModeFKSetup(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) [4]string {
	t.Helper()
	userOperatorExec(t, p, b, c,
		"CREATE TABLE tm_parent(id integer PRIMARY KEY)",
		"CREATE TABLE tm_child(id integer PRIMARY KEY,pid integer,CONSTRAINT tm_fk FOREIGN KEY(pid) REFERENCES tm_parent(id) ON DELETE CASCADE ON UPDATE CASCADE)")
	return triggerModeFKNames(t, p, c, "tm_fk")
}

func triggerModeFKError(q string, key int64) fixture.Step {
	return fixture.ErrorDiagnostic(q, fixture.ForeignKeyChild("tm_child", "tm_fk", "Key (pid)=("+triggerModeNumber(key)+") is not present in table \"tm_parent\"."))
}
