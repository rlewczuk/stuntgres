//go:build !noservertests

package reference_test

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgproto3"

	"stuntgres/reference/fixture"
)

func TestReferenceTransactionalTriggerProtocol(t *testing.T) {
	for _, commit := range []bool{false, true} {
		t.Run(fmt.Sprintf("extended/commit=%v", commit), func(t *testing.T) {
			f := fixture.NewServerFixture(t)
			for _, b := range f.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					w, peer := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b)), triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
					defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
					transactionalTriggerWireSeed(t, w)
					queries := []string{
						"CREATE TRIGGER a BEFORE INSERT ON txt_data FOR EACH ROW EXECUTE FUNCTION txt_add(3)",
						"CREATE OR REPLACE TRIGGER a BEFORE INSERT ON txt_data FOR EACH ROW EXECUTE FUNCTION txt_add(7)",
						`ALTER TRIGGER z ON txt_data RENAME TO "0double"`,
						"ALTER TRIGGER a ON txt_data DEPENDS ON EXTENSION unaccent",
						"ALTER TRIGGER a ON txt_data NO DEPENDS ON EXTENSION unaccent",
						"DROP TRIGGER a ON txt_data",
					}
					for i, q := range queries {
						frames := w.cycle(t, &pgproto3.Parse{Name: fmt.Sprint(i), Query: q}, &pgproto3.Describe{ObjectType: 'S', Name: fmt.Sprint(i)})
						triggerProtocolTags(t, frames, "1tnZ")
						triggerProtocolReady(t, frames, 'I')
						for _, frame := range frames {
							if frame[0] == 't' && string(frame[5:]) != "\x00\x00" {
								t.Fatalf("utility parameters %x", frame[5:])
							}
						}
					}
					reader := w.cycle(t, &pgproto3.Parse{Name: "insert", Query: "INSERT INTO txt_data VALUES($1,5) RETURNING id,v", ParameterOIDs: []uint32{23}}, &pgproto3.Describe{ObjectType: 'S', Name: "insert"})
					triggerProtocolTags(t, reader, "1tTZ")
					triggerProtocolDescription(t, reader, []uint32{23})
					triggerProtocolCode(t, w.query(t, "BEGIN"), "")
					id := 0
					insert := func(value int) {
						id++
						frames := w.cycle(t, &pgproto3.Bind{DestinationPortal: "row", PreparedStatement: "insert", Parameters: [][]byte{[]byte(strconv.Itoa(id))}}, &pgproto3.Describe{ObjectType: 'P', Name: "row"}, &pgproto3.Execute{Portal: "row"})
						triggerProtocolTags(t, frames, "2TDCZ")
						triggerProtocolReady(t, frames, 'T')
						triggerProtocolValues(t, frames, [][]string{{strconv.Itoa(id), strconv.Itoa(value)}})
						triggerProtocolCompletions(t, frames, "INSERT 0 1")
						triggerProtocolDescription(t, frames, nil)
						triggerProtocolCode(t, w.cycle(t, &pgproto3.Close{ObjectType: 'P', Name: "row"}), "")
					}
					before, after := []int{10, 16, 24, 17, 17, 17}, []int{16, 24, 17, 17, 17, 10}
					for i := range queries {
						portal := fmt.Sprintf("utility%d", i)
						frames := w.cycle(t, &pgproto3.Bind{DestinationPortal: portal, PreparedStatement: fmt.Sprint(i)}, &pgproto3.Describe{ObjectType: 'P', Name: portal})
						triggerProtocolTags(t, frames, "2nZ")
						triggerProtocolReady(t, frames, 'T')
						insert(before[i]) // Bind and Describe have not installed the utility.
						frames = w.cycle(t, &pgproto3.Execute{Portal: portal})
						triggerProtocolTags(t, frames, "CZ")
						triggerProtocolReady(t, frames, 'T')
						tag := "ALTER TRIGGER"
						if i < 2 {
							tag = "CREATE TRIGGER"
						}
						if i == 5 {
							tag = "DROP TRIGGER"
						}
						triggerProtocolCompletions(t, frames, tag)
						insert(after[i])
						if i == 0 {
							triggerProtocolCode(t, w.query(t, "SAVEPOINT chain"), "")
						}
					}
					triggerProtocolScalar(t, peer, "SELECT count(*) FROM pg_depend WHERE classid=2620 AND refclassid=1259 AND refobjid='txt_data'::regclass::oid AND deptype='a'", "1")
					triggerProtocolCode(t, w.query(t, "ROLLBACK TO chain; RELEASE chain"), "")
					insert(16)
					// Error/Ready E and rollback-to restore the prior private activation.
					triggerProtocolCode(t, w.query(t, "SAVEPOINT recovery"), "")
					frames := w.query(t, "SELECT 1/0")
					triggerProtocolError(t, frames, "22012", "division by zero", "")
					triggerProtocolReady(t, frames, 'E')
					frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "failed", PreparedStatement: "1"}, &pgproto3.Execute{Portal: "failed"})
					triggerProtocolError(t, frames, "25P02", "current transaction is aborted, commands ignored until end of transaction block", "")
					triggerProtocolReady(t, frames, 'E')
					triggerProtocolCode(t, w.query(t, "ROLLBACK TO recovery; RELEASE recovery"), "")
					insert(16)
					finish, want := "ROLLBACK", "10"
					if commit {
						finish, want = "COMMIT", "16"
					}
					frames = w.query(t, finish)
					triggerProtocolReady(t, frames, 'I')
					triggerProtocolCompletions(t, frames, finish)
					triggerProtocolScalar(t, peer, "INSERT INTO txt_data VALUES(99,5) RETURNING v", want)
					// The same named CREATE handle is usable after a full rollback.
					if !commit {
						frames = w.cycle(t, &pgproto3.Bind{DestinationPortal: "again", PreparedStatement: "0"}, &pgproto3.Execute{Portal: "again"})
						triggerProtocolCompletions(t, frames, "CREATE TRIGGER")
						triggerProtocolReady(t, frames, 'I')
						triggerProtocolScalar(t, w, "INSERT INTO txt_data VALUES(100,5) RETURNING v", "16")
					}
				})
			}
		})
	}
	t.Run("simple-messages", transactionalTriggerSimpleMessages)
	t.Run("simple-lifecycle", transactionalTriggerSimpleLifecycle)
	for _, held := range []bool{false, true} {
		t.Run(fmt.Sprintf("go/held=%v", held), func(t *testing.T) {
			f := fixture.NewServerFixture(t)
			for _, b := range f.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					w := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
					transactionalTriggerWireSeed(t, w)
					c, err := b.DB.Conn(f.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer c.Close()
					queries := []string{"CREATE TRIGGER a BEFORE INSERT ON txt_data FOR EACH ROW EXECUTE FUNCTION txt_add(3)", "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON txt_data FOR EACH ROW EXECUTE FUNCTION txt_add(7)", `ALTER TRIGGER z ON txt_data RENAME TO "0double"`, "ALTER TRIGGER a ON txt_data DEPENDS ON EXTENSION unaccent", "ALTER TRIGGER a ON txt_data NO DEPENDS ON EXTENSION unaccent", "DROP TRIGGER a ON txt_data"}
					stmts := map[string]*sql.Stmt{}
					if held {
						for _, q := range queries {
							s, e := c.PrepareContext(f.Ctx, q)
							if e != nil {
								t.Fatal(e)
							}
							defer s.Close()
							stmts[q] = s
						}
					}
					for _, commit := range []bool{false, true} {
						tx, err := c.BeginTx(f.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						for i, q := range queries {
							var e fixture.Executor = tx
							if held {
								bound := tx.StmtContext(f.Ctx, stmts[q])
								defer bound.Close()
								e = fixture.HeldStatement{Executor: tx, Statement: bound, SQL: q}
							}
							fixture.RunStep(t, f.Ctx, e, b.Recorder, fixture.Exec(q, 0))
							fixture.RunStep(t, f.Ctx, tx, b.Recorder, fixture.Select(fmt.Sprintf("INSERT INTO txt_data VALUES(%d,5) RETURNING v", i+1), []string{"v"}, []string{"INT4"}, [][]any{{[]int64{16, 24, 17, 17, 17, 10}[i]}}))
						}
						if commit {
							err = tx.Commit()
						} else {
							err = tx.Rollback()
						}
						if err != nil {
							t.Fatal(err)
						}
						triggerProtocolScalar(t, w, "INSERT INTO txt_data VALUES(99,5) RETURNING v", "10")
						triggerProtocolCode(t, w.query(t, "DELETE FROM txt_data WHERE id=99"), "")
					}
				})
			}
		})
	}
}

func transactionalTriggerWireSeed(t *testing.T, w *triggerProtocolWire) {
	t.Helper()
	for _, q := range []string{"CREATE TABLE txt_data(id integer PRIMARY KEY,v integer)", "CREATE FUNCTION txt_add() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v+TG_ARGV[0]::integer; RETURN NEW; END$$", "CREATE FUNCTION txt_double() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN NEW.v:=NEW.v*2; RETURN NEW; END$$", "CREATE TRIGGER z BEFORE INSERT ON txt_data FOR EACH ROW EXECUTE FUNCTION txt_double()", "CREATE EXTENSION unaccent"} {
		triggerProtocolCode(t, w.query(t, q), "")
	}
}

func transactionalTriggerSimpleMessages(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			w, peer := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b)), triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
			transactionalTriggerWireSeed(t, w)
			create := "CREATE TRIGGER a BEFORE INSERT ON txt_data FOR EACH ROW EXECUTE FUNCTION txt_add(3)"
			frames := w.query(t, create+"; INSERT INTO txt_data VALUES(1,5); SELECT 1/0; INSERT INTO txt_data VALUES(2,5)")
			triggerProtocolError(t, frames, "22012", "division by zero", "")
			triggerProtocolReady(t, frames, 'I')
			triggerProtocolScalar(t, peer, "SELECT count(*) FROM txt_data", "0")
			triggerProtocolScalar(t, w, "INSERT INTO txt_data VALUES(3,5) RETURNING v", "10")
			frames = w.query(t, create+"; INSERT INTO txt_data VALUES(4,5); BEGIN")
			triggerProtocolCode(t, frames, "")
			triggerProtocolReady(t, frames, 'T')
			triggerProtocolScalar(t, peer, "SELECT count(*) FROM txt_data", "1")
			triggerProtocolScalar(t, w, "SELECT v FROM txt_data WHERE id=4", "16")
			triggerProtocolCode(t, w.query(t, "ROLLBACK"), "")
			triggerProtocolScalar(t, peer, "SELECT count(*) FROM txt_data", "1")
			frames = w.query(t, create+"; INSERT INTO txt_data VALUES(5,5); BEGIN; COMMIT; SELECT 1/0")
			triggerProtocolError(t, frames, "22012", "division by zero", "")
			triggerProtocolReady(t, frames, 'I')
			triggerProtocolScalar(t, peer, "SELECT v FROM txt_data WHERE id=5", "16")
			triggerProtocolScalar(t, peer, "INSERT INTO txt_data VALUES(6,5) RETURNING v", "16")
			triggerProtocolCode(t, w.query(t, "DROP TRIGGER a ON txt_data"), "")
			bad := create + "; INSERT INTO txt_data VALUES(7,5); COMMIT; SELECT FROM"
			frames = w.query(t, bad)
			triggerProtocolError(t, frames, "42601", "syntax error at end of input", strconv.Itoa(len(bad)+1))
			triggerProtocolReady(t, frames, 'I')
			triggerProtocolScalar(t, peer, "SELECT count(*) FROM txt_data WHERE id=7", "0")
			triggerProtocolScalar(t, w, "INSERT INTO txt_data VALUES(8,5) RETURNING v", "10")
			frames = w.query(t, create+"; INSERT INTO txt_data VALUES(9,5)")
			triggerProtocolCode(t, frames, "")
			triggerProtocolCompletions(t, frames, "CREATE TRIGGER", "INSERT 0 1")
			triggerProtocolReady(t, frames, 'I')
			triggerProtocolScalar(t, peer, "SELECT v FROM txt_data WHERE id=9", "16")
		})
	}
}

func transactionalTriggerSimpleLifecycle(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, commit := range []bool{false, true} {
			if !explicit && !commit {
				continue
			}
			t.Run(fmt.Sprintf("explicit=%v/commit=%v", explicit, commit), func(t *testing.T) {
				f := fixture.NewServerFixture(t)
				for _, b := range f.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						w, peer := triggerProtocolConnect(t, f.Ctx, f.DSNFor(b)), triggerProtocolConnect(t, f.Ctx, f.DSNFor(b))
						transactionalTriggerWireSeed(t, w)
						defer func() { triggerProtocolCode(t, w.query(t, "ROLLBACK"), "") }()
						ready := byte('I')
						if explicit {
							triggerProtocolCode(t, w.query(t, "BEGIN"), "")
							ready = 'T'
						}
						queries := []string{"CREATE TRIGGER a BEFORE INSERT ON txt_data FOR EACH ROW EXECUTE FUNCTION txt_add(3)", "CREATE OR REPLACE TRIGGER a BEFORE INSERT ON txt_data FOR EACH ROW EXECUTE FUNCTION txt_add(7)", `ALTER TRIGGER z ON txt_data RENAME TO "0double"`, "ALTER TRIGGER a ON txt_data DEPENDS ON EXTENSION unaccent", "ALTER TRIGGER a ON txt_data NO DEPENDS ON EXTENSION unaccent", "DROP TRIGGER a ON txt_data"}
						for i, q := range queries {
							frames := w.query(t, q)
							triggerProtocolCode(t, frames, "")
							triggerProtocolReady(t, frames, ready)
							tag := "ALTER TRIGGER"
							if i < 2 {
								tag = "CREATE TRIGGER"
							}
							if i == 5 {
								tag = "DROP TRIGGER"
							}
							triggerProtocolCompletions(t, frames, tag)
							frames = w.query(t, fmt.Sprintf("INSERT INTO txt_data VALUES(%d,5) RETURNING id,v", i+1))
							triggerProtocolReady(t, frames, ready)
							triggerProtocolCompletions(t, frames, "INSERT 0 1")
							triggerProtocolValues(t, frames, [][]string{{strconv.Itoa(i + 1), []string{"16", "24", "17", "17", "17", "10"}[i]}})
						}
						if explicit {
							end := "ROLLBACK"
							if commit {
								end = "COMMIT"
							}
							frames := w.query(t, end)
							triggerProtocolReady(t, frames, 'I')
							triggerProtocolCompletions(t, frames, end)
						}
						count := "0"
						if commit {
							count = "6"
						}
						triggerProtocolScalar(t, peer, "SELECT count(*) FROM txt_data", count)
						triggerProtocolScalar(t, peer, "INSERT INTO txt_data VALUES(99,5) RETURNING v", "10")
					})
				}
			})
		}
	}
}

// pgconn owns startup/authentication and transport. Only this scenario's actual
// protocol phases are sent here; no driver query mode or prepare surrogate is used.
type triggerProtocolWire struct {
	conn *pgconn.PgConn
	ctx  context.Context
}

func triggerProtocolConnect(t *testing.T, ctx context.Context, dsn string) *triggerProtocolWire {
	t.Helper()
	conn, err := pgconn.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := conn.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return &triggerProtocolWire{conn: conn, ctx: ctx}
}

func (w *triggerProtocolWire) query(t *testing.T, q string) [][]byte {
	t.Helper()
	w.conn.Frontend().Send(&pgproto3.Query{String: q})
	return w.receive(t)
}

func (w *triggerProtocolWire) cycle(t *testing.T, messages ...pgproto3.FrontendMessage) [][]byte {
	t.Helper()
	for _, message := range messages {
		w.conn.Frontend().Send(message)
	}
	w.conn.Frontend().Send(&pgproto3.Sync{})
	return w.receive(t)
}

func (w *triggerProtocolWire) receive(t *testing.T) [][]byte {
	t.Helper()
	if err := w.conn.Frontend().Flush(); err != nil {
		t.Fatal(err)
	}
	var frames [][]byte
	for {
		message, err := w.conn.ReceiveMessage(w.ctx)
		if err != nil {
			t.Fatal(err)
		}
		// ReceiveMessage may reuse flyweight fields. Encode before the next receive
		// so descriptors, diagnostics and row bytes have independent owned storage.
		frame, err := message.Encode(nil)
		if err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
		if _, ready := message.(*pgproto3.ReadyForQuery); ready {
			return frames
		}
	}
}

func triggerProtocolTags(t *testing.T, frames [][]byte, want string) {
	t.Helper()
	var tags []byte
	for _, frame := range frames {
		tags = append(tags, frame[0])
	}
	if string(tags) != want {
		t.Fatalf("response tags %q want %q", tags, want)
	}
}

func triggerProtocolReady(t *testing.T, frames [][]byte, want byte) {
	t.Helper()
	if len(frames) == 0 || frames[len(frames)-1][0] != 'Z' {
		t.Fatal("missing ReadyForQuery")
	}
	var ready pgproto3.ReadyForQuery
	if err := ready.Decode(frames[len(frames)-1][5:]); err != nil {
		t.Fatal(err)
	}
	if ready.TxStatus != want {
		t.Fatalf("ReadyForQuery %c want %c", ready.TxStatus, want)
	}
}

func triggerProtocolCode(t *testing.T, frames [][]byte, want string) {
	t.Helper()
	count := 0
	for _, frame := range frames {
		if frame[0] != 'E' {
			continue
		}
		var diagnostic pgproto3.ErrorResponse
		if err := diagnostic.Decode(frame[5:]); err != nil {
			t.Fatal(err)
		}
		count++
		if diagnostic.Code != want {
			t.Fatalf("error %s: %s; want %q", diagnostic.Code, diagnostic.Message, want)
		}
	}
	if (want != "" && count != 1) || (want == "" && count != 0) {
		t.Fatalf("error count %d for %q", count, want)
	}
}

func triggerProtocolValues(t *testing.T, frames [][]byte, want [][]string) {
	t.Helper()
	triggerProtocolCode(t, frames, "")
	var rows [][]string
	for _, frame := range frames {
		if frame[0] != 'D' {
			continue
		}
		var data pgproto3.DataRow
		if err := data.Decode(frame[5:]); err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(data.Values))
		for i, value := range data.Values {
			if value == nil {
				t.Fatal("unexpected NULL trigger result")
			}
			row[i] = string(value)
		}
		rows = append(rows, row)
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows %#v want %#v", rows, want)
	}
}

func triggerProtocolCompletions(t *testing.T, frames [][]byte, want ...string) {
	t.Helper()
	var tags []string
	for _, frame := range frames {
		if frame[0] != 'C' {
			continue
		}
		var complete pgproto3.CommandComplete
		if err := complete.Decode(frame[5:]); err != nil {
			t.Fatal(err)
		}
		tags = append(tags, string(complete.CommandTag))
	}
	if !reflect.DeepEqual(tags, want) {
		t.Fatalf("command tags %#v want %#v", tags, want)
	}
}

func triggerProtocolScalar(t *testing.T, w *triggerProtocolWire, q, want string) {
	t.Helper()
	triggerProtocolValues(t, w.query(t, q), [][]string{{want}})
}

func triggerProtocolDescription(t *testing.T, frames [][]byte, parameters []uint32) {
	t.Helper()
	triggerProtocolCode(t, frames, "")
	parameterCount, resultCount := 0, 0
	for _, frame := range frames {
		switch frame[0] {
		case 't':
			var description pgproto3.ParameterDescription
			if err := description.Decode(frame[5:]); err != nil {
				t.Fatal(err)
			}
			parameterCount++
			if !reflect.DeepEqual(description.ParameterOIDs, parameters) {
				t.Fatalf("parameter OIDs %v want %v", description.ParameterOIDs, parameters)
			}
		case 'T':
			var description pgproto3.RowDescription
			if err := description.Decode(frame[5:]); err != nil {
				t.Fatal(err)
			}
			resultCount++
			if len(description.Fields) != 2 {
				t.Fatal(description.Fields)
			}
			for i, field := range description.Fields {
				if string(field.Name) != []string{"id", "v"}[i] || field.DataTypeOID != 23 {
					t.Fatal(description.Fields)
				}
			}
		}
	}
	wantParameters := 0
	if parameters != nil {
		wantParameters = 1
	}
	if parameterCount != wantParameters || resultCount != 1 {
		t.Fatalf("description counts parameters=%d results=%d", parameterCount, resultCount)
	}
}

func triggerProtocolError(t *testing.T, frames [][]byte, code, message, position string) {
	t.Helper()
	triggerProtocolCode(t, frames, code)
	want := fixture.ControlsReferenceDiagnostic(code, message)
	want.Position = position
	for _, frame := range frames {
		if frame[0] != 'E' {
			continue
		}
		var d pgproto3.ErrorResponse
		if err := d.Decode(frame[5:]); err != nil {
			t.Fatal(err)
		}
		got := fixture.Diagnostic{Severity: d.Severity, SeverityUnlocalized: d.SeverityUnlocalized, Code: d.Code, Message: d.Message, Detail: d.Detail, Hint: d.Hint, Position: fixture.Position(int(d.Position)), InternalPosition: fixture.Position(int(d.InternalPosition)), InternalQuery: d.InternalQuery, Context: d.Where, Schema: d.SchemaName, Table: d.TableName, Column: d.ColumnName, DataType: d.DataTypeName, Constraint: d.ConstraintName}
		if got != want {
			t.Fatalf("diagnostic %#v want %#v", got, want)
		}
	}
}
