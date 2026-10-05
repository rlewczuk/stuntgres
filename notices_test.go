package stuntgres_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func noticePool(t *testing.T, base *stuntgres.Connector, handler func(stuntgres.Error)) (*sql.DB, *sql.Conn) {
	t.Helper()
	db := sql.OpenDB(base.WithNoticeHandler(handler))
	t.Cleanup(func() { db.Close() })
	c, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return db, c
}
func noticeExec(t *testing.T, c *sql.Conn, q string) {
	t.Helper()
	noticeExecContext(t, context.Background(), c, q)
}
func noticeExecContext(t *testing.T, ctx context.Context, c *sql.Conn, q string) {
	t.Helper()
	r, err := c.ExecContext(ctx, q)
	if err != nil {
		t.Fatal(q, err)
	}
	n, err := r.RowsAffected()
	if err != nil || n != 0 {
		t.Fatal(q, n, err)
	}
}
func noticeError(t *testing.T, err error, code, message, hint string) {
	t.Helper()
	var got *stuntgres.Error
	want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint}
	if !errors.As(err, &got) || *got != want {
		t.Fatalf("got %#v %v want %#v", got, err, want)
	}
}
func noticeShow(t *testing.T, c *sql.Conn, name, want string) {
	t.Helper()
	noticeShowContext(t, context.Background(), c, name, want)
}
func noticeShowContext(t *testing.T, ctx context.Context, c *sql.Conn, name, want string) {
	t.Helper()
	rows, err := c.QueryContext(ctx, "SHOW "+name)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 1 || types[0].Name() != name || types[0].DatabaseTypeName() != "TEXT" {
		t.Fatal(types, err)
	}
	var got string
	if !rows.Next() {
		t.Fatal("missing SHOW")
	}
	if err = rows.Scan(&got); err != nil || got != want || rows.Next() || rows.Err() != nil {
		t.Fatal(got, err, rows.Err())
	}
}
func noticeRows(t *testing.T, c *sql.Conn) {
	t.Helper()
	noticeRowsContext(t, context.Background(), c)
}
func noticeRowsContext(t *testing.T, ctx context.Context, c *sql.Conn) {
	t.Helper()
	rows, err := c.QueryContext(ctx, "SELECT * FROM notice_rows ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
		t.Fatal(types, err)
	}
	var id int
	var note string
	if !rows.Next() {
		t.Fatal("missing row")
	}
	if err = rows.Scan(&id, &note); err != nil || id != 7 || note != "kept" || rows.Next() || rows.Err() != nil {
		t.Fatal(id, note, err, rows.Err())
	}
}
func TestNoticesCommands(t *testing.T) {
	for _, mode := range []string{"exec", "query", "prepared-exec", "prepared-query"} {
		t.Run(mode, func(t *testing.T) {
			raw, _ := stuntgres.NewDriver().OpenConnector(t.Name())
			var notices []stuntgres.Error
			_, c := noticePool(t, raw.(*stuntgres.Connector), func(n stuntgres.Error) { notices = append(notices, n) })
			noticeExec(t, c, "CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)")
			r, err := c.ExecContext(context.Background(), "INSERT INTO notice_rows VALUES(7,'kept')")
			if err != nil {
				t.Fatal(err)
			}
			count, _ := r.RowsAffected()
			if count != 1 {
				t.Fatal(count)
			}
			execute := func(q string) error {
				var stmt *sql.Stmt
				if strings.HasPrefix(mode, "prepared") {
					var err error
					before := len(notices)
					stmt, err = c.PrepareContext(context.Background(), q)
					if err != nil {
						t.Fatal("semantic failure during preparation", err)
					}
					defer stmt.Close()
					if len(notices) != before {
						t.Fatal("prepare delivered")
					}
				}
				if strings.HasSuffix(mode, "query") {
					var rows *sql.Rows
					var err error
					if stmt != nil {
						rows, err = stmt.Query()
					} else {
						rows, err = c.QueryContext(context.Background(), q)
					}
					if err != nil {
						return err
					}
					defer rows.Close()
					if rows.Next() || rows.Err() != nil {
						t.Fatal("command rows", rows.Err())
					}
					return nil
				}
				var r sql.Result
				var err error
				if stmt != nil {
					r, err = stmt.Exec()
				} else {
					r, err = c.ExecContext(context.Background(), q)
				}
				if err == nil {
					n, e := r.RowsAffected()
					if n != 0 || e != nil {
						t.Fatal(n, e)
					}
				}
				return err
			}
			want := stuntgres.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}
			for _, q := range []string{"CREATE TABLE IF NOT EXISTS notice_rows(replaced BOOL)", "CREATE TABLE IF NOT EXISTS public.notice_rows(id INT,id TEXT)"} {
				if err = execute(q); err != nil {
					t.Fatal(err)
				}
				if len(notices) == 0 || notices[len(notices)-1] != want {
					t.Fatal(notices)
				}
				noticeRows(t, c)
			}
			if len(notices) != 2 {
				t.Fatal(notices)
			}
			noticeError(t, execute("CREATE TABLE IF NOT EXISTS fresh(id INT,id TEXT)"), "42701", `column "id" specified more than once`, "")
			if len(notices) != 2 {
				t.Fatal("semantic failure emitted notice", notices)
			}
			if err = execute("CREATE TABLE IF NOT EXISTS fresh(id INT)"); err != nil {
				t.Fatal(err)
			}
			noticeExec(t, c, "SET client_min_messages TO error")
			noticeError(t, execute("CREATE TABLE notice_rows(id INT)"), "42P07", `relation "notice_rows" already exists`, "")
			if err = execute("CREATE TABLE IF NOT EXISTS notice_rows(id INT)"); err != nil || len(notices) != 2 {
				t.Fatal(err, notices)
			}
			noticeError(t, execute("CREATE TABLE IF NOT EXISTS missing_schema.x(id INT)"), "3F000", `schema "missing_schema" does not exist`, "")
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, err = c.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS canceled(id INT)")
			if !errors.Is(err, context.Canceled) || len(notices) != 2 {
				t.Fatal(err, notices)
			}
			noticeRows(t, c)
		})
	}
}
func TestNoticesSettings(t *testing.T) {
	raw, _ := stuntgres.NewDriver().OpenConnector(t.Name())
	var notices []stuntgres.Error
	_, a := noticePool(t, raw.(*stuntgres.Connector), func(n stuntgres.Error) { notices = append(notices, n) })
	_, b := noticePool(t, raw.(*stuntgres.Connector), nil)
	noticeShow(t, a, "client_min_messages", "notice")
	for _, tc := range []struct{ input, want string }{{"WARNING", "warning"}, {"debug", "debug2"}, {"info", "info"}, {"DEFAULT", "notice"}} {
		noticeExec(t, a, "SET client_min_messages TO "+tc.input)
		noticeShow(t, a, "client_min_messages", tc.want)
		noticeShow(t, b, "client_min_messages", "notice")
	}
	noticeExec(t, a, "SET client_min_messages TO warning")
	for _, prepared := range []bool{false, true} {
		before := len(notices)
		var err error
		if prepared {
			stmt, prepareErr := a.PrepareContext(context.Background(), "SET client_min_messages TO bogus")
			if prepareErr != nil {
				t.Fatal("semantic failure during preparation", prepareErr)
			}
			if len(notices) != before {
				stmt.Close()
				t.Fatal("preparation emitted notice")
			}
			_, err = stmt.ExecContext(context.Background())
			if closeErr := stmt.Close(); closeErr != nil {
				t.Fatal(closeErr)
			}
		} else {
			_, err = a.ExecContext(context.Background(), "SET client_min_messages TO bogus")
		}
		noticeError(t, err, "22023", `invalid value for parameter "client_min_messages": "bogus"`, "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error.")
		noticeShow(t, a, "client_min_messages", "warning")
		if len(notices) != before {
			t.Fatal("invalid threshold emitted notice", notices[before:])
		}
	}
	_, err := a.ExecContext(context.Background(), "SET client_min_messages TO notice,warning")
	noticeError(t, err, "22023", "SET client_min_messages takes only one argument", "")
	for _, threshold := range []string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error", "debug"} {
		noticeExec(t, a, "SET client_min_messages TO "+threshold)
		before := len(notices)
		original := strings.Repeat("a", 64)
		noticeExec(t, a, "SET application_name TO '"+original+"'")
		wantDelivery := threshold != "warning" && threshold != "error"
		if (len(notices) == before+1) != wantDelivery {
			t.Fatal(threshold, notices)
		}
		if wantDelivery {
			want := stuntgres.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + strings.Repeat("a", 63) + `"`}
			if notices[before] != want {
				t.Fatal(notices[before])
			}
		}
		noticeShow(t, a, "application_name", strings.Repeat("a", 63))
	}
	noticeExec(t, a, "SET client_min_messages TO DEFAULT")
	noticeExec(t, a, "SET application_name TO '"+strings.Repeat("a", 62)+"é'")
	noticeShow(t, a, "application_name", strings.Repeat("a", 62))
	noticeExec(t, a, "SET application_name TO 'é'")
	noticeShow(t, a, "application_name", `\xc3\xa9`)
}
func TestNoticesReentrant(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	raw, _ := stuntgres.NewDriver().OpenConnector(t.Name())
	base := raw.(*stuntgres.Connector)
	peerDB := sql.OpenDB(base)
	b, err := peerDB.Conn(ctx)
	if err != nil {
		peerDB.Close()
		t.Fatal(err)
	}
	var notices []stuntgres.Error
	sourceDB := sql.OpenDB(base.WithNoticeHandler(func(n stuntgres.Error) {
		noticeRowsContext(t, ctx, b)
		noticeShowContext(t, ctx, b, "application_name", "")
		noticeShowContext(t, ctx, b, "client_min_messages", "notice")
		notices = append(notices, n)
		n.Message = "changed"
		n.Detail = "mutated"
	}))
	a, err := sourceDB.Conn(ctx)
	if err != nil {
		b.Close()
		peerDB.Close()
		sourceDB.Close()
		t.Fatal(err)
	}
	joined := false
	defer func() {
		cleanup := func() { a.Close(); b.Close(); sourceDB.Close(); peerDB.Close() }
		if joined {
			cleanup()
		} else {
			// A suspected callback/lease deadlock must not make failure cleanup
			// synchronously wait on the very operations this witness bounds.
			go cleanup()
		}
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		noticeExecContext(t, ctx, a, "CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)")
		if _, err := a.ExecContext(ctx, "INSERT INTO notice_rows VALUES(7,'kept')"); err != nil {
			t.Error(err)
			return
		}
		noticeExecContext(t, ctx, a, "CREATE TABLE IF NOT EXISTS notice_rows(x BOOL)")
		noticeExecContext(t, ctx, a, "CREATE TABLE IF NOT EXISTS notice_rows(x BOOL)")
	}()
	select {
	case <-done:
		joined = true
	case <-ctx.Done():
		t.Fatal("peer reentry failed to join within deadline", ctx.Err())
	}
	if t.Failed() {
		return
	}
	want := stuntgres.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}
	if !reflect.DeepEqual(notices, []stuntgres.Error{want, want}) {
		t.Fatal(notices)
	}
	// Raw physical Close is safe because dispatch has released the lease.
	// Observe the whole command (including Close) from outside the callback.
	var physical driver.Conn
	var closeErr error
	physical, err = base.WithNoticeHandler(func(n stuntgres.Error) {
		closeErr = physical.Close()
	}).Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	joined = false
	returned := make(chan error, 1)
	go func() {
		_, err := physical.(driver.ExecerContext).ExecContext(ctx, "CREATE TABLE IF NOT EXISTS notice_rows(x BOOL)", nil)
		returned <- err
	}()
	select {
	case err = <-returned:
		joined = true
		if err != nil || closeErr != nil {
			t.Fatal("raw physical Close reentry", err, closeErr)
		}
	case <-ctx.Done():
		// Do not synchronously Close again: the callback may already be
		// blocked in Close waiting for an incorrectly retained operation lease.
		go physical.Close()
		t.Fatal("raw physical Close failed to join within deadline", ctx.Err())
	}
}
func TestNoticesSynchronous(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{})
	release := make(chan struct{})
	returned := make(chan error, 1)
	raw, _ := stuntgres.NewDriver().OpenConnector(t.Name())
	db := sql.OpenDB(raw.(*stuntgres.Connector).WithNoticeHandler(func(n stuntgres.Error) { close(entered); <-release }))
	defer db.Close()
	go func() {
		_, err := db.ExecContext(ctx, "SET application_name TO '"+strings.Repeat("a", 64)+"'")
		returned <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		close(release)
		t.Fatal(ctx.Err())
	}
	select {
	case err := <-returned:
		close(release)
		t.Fatal("returned before callback", err)
	default:
	}
	cancel()
	close(release)
	if err := <-returned; err != nil {
		t.Fatal("execution result replaced", err)
	}
}
func TestNoticesControls(t *testing.T) {
	raw, _ := stuntgres.NewDriver().OpenConnector(t.Name())
	var notices []stuntgres.Error
	db, a := noticePool(t, raw.(*stuntgres.Connector), func(n stuntgres.Error) { notices = append(notices, n) })
	noticeExec(t, a, "CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)")
	if _, err := a.ExecContext(context.Background(), "INSERT INTO notice_rows VALUES(7,'kept')"); err != nil {
		t.Fatal(err)
	}
	noticeExec(t, a, "CREATE TABLE IF NOT EXISTS notice_rows(x BOOL)")
	var dump bytes.Buffer
	if err := stuntgres.Dump(context.Background(), db, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "IF NOT EXISTS") {
		t.Fatal(dump.String())
	}
	noticeExec(t, a, "SET client_min_messages TO warning")
	if err := stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(context.Background(), db, strings.NewReader(dump.String()+"CREATE TABLE IF NOT EXISTS notice_rows(x BOOL);")); err != nil {
		t.Fatal(err)
	}
	noticeRows(t, a)
	noticeShow(t, a, "client_min_messages", "warning")
	if len(notices) != 1 {
		t.Fatal(notices)
	}
	err := stuntgres.Restore(context.Background(), db, strings.NewReader("CREATE TABLE bad(id INT NOT NULL); INSERT INTO bad VALUES(NULL);"))
	if err == nil {
		t.Fatal("restore succeeded")
	}
	noticeRows(t, a)
}
func TestNoticesIsolation(t *testing.T) {
	d := stuntgres.NewDriver()
	raw, _ := d.OpenConnector(t.Name())
	base := raw.(*stuntgres.Connector)
	counts := [2]int{}
	_, a := noticePool(t, base, func(n stuntgres.Error) { counts[0]++ })
	_, b := noticePool(t, base, func(n stuntgres.Error) { counts[1]++ })
	_, silent := noticePool(t, base, nil)
	noticeExec(t, a, "CREATE TABLE shared(id INT)")
	for _, c := range []*sql.Conn{a, b, silent} {
		noticeExec(t, c, "CREATE TABLE IF NOT EXISTS shared(other TEXT)")
	}
	if counts != [2]int{1, 1} {
		t.Fatal(counts)
	}
	noticeExec(t, a, "SET client_min_messages TO error")
	noticeExec(t, a, "CREATE TABLE IF NOT EXISTS shared(id INT)")
	noticeExec(t, b, "CREATE TABLE IF NOT EXISTS shared(id INT)")
	if counts != [2]int{1, 2} {
		t.Fatal(counts)
	}
	for i, other := range []*stuntgres.Driver{d, stuntgres.NewDriver()} {
		dsn := t.Name()
		if i == 0 {
			dsn += "/other"
		}
		r, _ := other.OpenConnector(dsn)
		_, c := noticePool(t, r.(*stuntgres.Connector), nil)
		_, err := c.ExecContext(context.Background(), "SELECT * FROM shared")
		if err == nil {
			t.Fatal(fmt.Sprint("data leak ", i))
		}
	}
}

func TestNoticesPhysicalReplacement(t *testing.T) {
	raw, _ := stuntgres.NewDriver().OpenConnector(t.Name())
	base := raw.(*stuntgres.Connector)
	first, second := 0, 0
	configured := base.WithNoticeHandler(func(n stuntgres.Error) { first++ })
	db := sql.OpenDB(configured)
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	ctx := context.Background()
	a, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	noticeExec(t, a, "CREATE TABLE replacement(id INT)")
	if _, err = a.ExecContext(ctx, "INSERT INTO replacement VALUES(7)"); err != nil {
		t.Fatal(err)
	}
	noticeExec(t, a, "SET client_min_messages TO warning")
	a.Close()
	a, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	noticeShow(t, a, "client_min_messages", "warning")
	// Reconfiguration does not retrofit an already connected physical session.
	changed := configured.WithNoticeHandler(func(n stuntgres.Error) { second++ })
	other := sql.OpenDB(changed)
	defer other.Close()
	noticeExec(t, a, "SET client_min_messages TO notice")
	noticeExec(t, a, "CREATE TABLE IF NOT EXISTS replacement(id INT)")
	if first != 1 || second != 0 {
		t.Fatal(first, second)
	}
	if _, err = other.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS replacement(id INT)"); err != nil {
		t.Fatal(err)
	}
	if first != 1 || second != 1 {
		t.Fatal(first, second)
	}
	noticeExec(t, a, "SET client_min_messages TO error")
	err = a.Raw(func(any) error { return driver.ErrBadConn })
	if !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	a.Close()
	a, err = db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	noticeShow(t, a, "client_min_messages", "notice")
	noticeExec(t, a, "CREATE TABLE IF NOT EXISTS replacement(id INT)")
	if first != 2 || second != 1 {
		t.Fatal(first, second)
	}
	var got int
	if err = a.QueryRowContext(ctx, "SELECT id FROM replacement").Scan(&got); err != nil || got != 7 {
		t.Fatal(got, err)
	}
}

func TestNoticesApplicationName(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, query := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepared=%v/query=%v", prepared, query), func(t *testing.T) {
				raw, _ := stuntgres.NewDriver().OpenConnector(t.Name())
				var notices []stuntgres.Error
				_, c := noticePool(t, raw.(*stuntgres.Connector), func(n stuntgres.Error) { notices = append(notices, n) })
				original := strings.Repeat("a", 64)
				clipped := strings.Repeat("a", 63)
				q := "SET application_name TO '" + original + "'"
				var stmt *sql.Stmt
				if prepared {
					var err error
					stmt, err = c.PrepareContext(context.Background(), q)
					if err != nil {
						t.Fatal(err)
					}
					defer stmt.Close()
					if len(notices) != 0 {
						t.Fatal("prepare emitted")
					}
				}
				if query {
					var rows *sql.Rows
					var err error
					if stmt != nil {
						rows, err = stmt.Query()
					} else {
						rows, err = c.QueryContext(context.Background(), q)
					}
					if err != nil {
						t.Fatal(err)
					}
					if rows.Next() || rows.Err() != nil {
						rows.Close()
						t.Fatal(rows.Err())
					}
					rows.Close()
				} else {
					var r sql.Result
					var err error
					if stmt != nil {
						r, err = stmt.Exec()
					} else {
						r, err = c.ExecContext(context.Background(), q)
					}
					if err != nil {
						t.Fatal(err)
					}
					n, e := r.RowsAffected()
					if n != 0 || e != nil {
						t.Fatal(n, e)
					}
				}
				want := []stuntgres.Error{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}}
				if !reflect.DeepEqual(notices, want) {
					t.Fatal(notices)
				}
				noticeShow(t, c, "application_name", clipped)
				canceled, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := c.ExecContext(canceled, "SET application_name TO changed")
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
				noticeShow(t, c, "application_name", clipped)
				if !reflect.DeepEqual(notices, want) {
					t.Fatal(notices)
				}
			})
		}
	}
}

func TestNoticesRetainedSnapshot(t *testing.T) {
	raw, _ := stuntgres.NewDriver().OpenConnector(t.Name())
	db, c := noticePool(t, raw.(*stuntgres.Connector), nil)
	noticeExec(t, c, "CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)")
	if _, err := c.ExecContext(context.Background(), "INSERT INTO notice_rows VALUES(7,'kept')"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := c.QueryContext(context.Background(), "SELECT * FROM notice_rows")
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if err = stuntgres.Reset(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var id int
	var note string
	if !snapshot.Next() {
		t.Fatal("snapshot erased")
	}
	if err = snapshot.Scan(&id, &note); err != nil || id != 7 || note != "kept" || snapshot.Next() || snapshot.Err() != nil {
		t.Fatal(id, note, err, snapshot.Err())
	}
}

func TestNoticesCancellationAndSyntax(t *testing.T) {
	raw, _ := stuntgres.NewDriver().OpenConnector(t.Name())
	count := 0
	_, c := noticePool(t, raw.(*stuntgres.Connector), func(n stuntgres.Error) { count++ })
	noticeExec(t, c, "CREATE TABLE cancel_rows(id INT)")
	stmt, err := c.PrepareContext(context.Background(), "CREATE TABLE IF NOT EXISTS cancel_rows(other TEXT)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, query := range []bool{false, true} {
		if query {
			rows, e := stmt.QueryContext(ctx)
			err = e
			if rows != nil {
				rows.Close()
			}
		} else {
			_, err = stmt.ExecContext(ctx)
		}
		if !errors.Is(err, context.Canceled) || count != 0 {
			t.Fatal(err, count)
		}
	}
	_, err = c.ExecContext(context.Background(), "CREATE TABLE IF NOT EXISTS syntax_prefix(id INT) garbage")
	var diagnostic *stuntgres.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "42601" || diagnostic.Position == "" || count != 0 {
		t.Fatal(err, count)
	}
	// A complete valid command can still create the name: no prefix was published.
	noticeExec(t, c, "CREATE TABLE syntax_prefix(id INT)")
	noticeExec(t, c, "CREATE TABLE IF NOT EXISTS cancel_rows(other TEXT)")
	if count != 1 {
		t.Fatal(count)
	}
}

func TestNoticesFilter(t *testing.T) {
	raw, _ := stuntgres.NewDriver().OpenConnector(t.Name())
	var notices []stuntgres.Error
	_, c := noticePool(t, raw.(*stuntgres.Connector), func(n stuntgres.Error) { notices = append(notices, n) })
	noticeExec(t, c, "CREATE TABLE filtered(id INT)")
	for _, threshold := range []string{"debug5", "debug4", "debug3", "debug2", "debug1", "log", "info", "notice", "warning", "error", "debug"} {
		noticeExec(t, c, "SET client_min_messages TO "+threshold)
		before := len(notices)
		noticeExec(t, c, "CREATE TABLE IF NOT EXISTS filtered(other TEXT)")
		delivered := threshold != "warning" && threshold != "error"
		if (len(notices) == before+1) != delivered {
			t.Fatal(threshold, notices)
		}
		_, err := c.ExecContext(context.Background(), "CREATE TABLE filtered(other TEXT)")
		noticeError(t, err, "42P07", `relation "filtered" already exists`, "")
		expected := before
		if delivered {
			expected++
		}
		if len(notices) != expected {
			t.Fatal("error transported as notice", notices)
		}
	}
}

func TestNoticesPreparedShow(t *testing.T) {
	raw, _ := stuntgres.NewDriver().OpenConnector(t.Name())
	_, c := noticePool(t, raw.(*stuntgres.Connector), nil)
	stmt, err := c.PrepareContext(context.Background(), "SHOW client_min_messages")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for _, want := range []string{"warning", "debug2", "notice"} {
		noticeExec(t, c, "SET client_min_messages TO "+want)
		var got string
		if err = stmt.QueryRow().Scan(&got); err != nil || got != want {
			t.Fatal(got, err)
		}
		result, e := stmt.Exec()
		if e != nil {
			t.Fatal(e)
		}
		count, e := result.RowsAffected()
		if count != 0 || e != nil {
			t.Fatal(count, e)
		}
	}
}
