package smoke_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"stuntgres"
	"testing"
	"time"
)

func noticesRun() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	base, err := stuntgres.NewDriver().OpenConnector("notices-smoke")
	if err != nil {
		return err
	}
	connector := base.(*stuntgres.Connector)
	peerPool := sql.OpenDB(connector)
	defer peerPool.Close()
	peer, err := peerPool.Conn(ctx)
	if err != nil {
		return err
	}
	defer peer.Close()
	var notices []stuntgres.Error
	var callbackErr error
	db := sql.OpenDB(connector.WithNoticeHandler(func(n stuntgres.Error) {
		var id int
		var note string
		if err := peer.QueryRowContext(ctx, "SELECT id,note FROM notice_rows").Scan(&id, &note); err != nil {
			callbackErr = err
		} else if id != 7 || note != "kept" {
			callbackErr = fmt.Errorf("peer row %d %q", id, note)
		}
		notices = append(notices, n)
		n.Message = "local mutation"
	}))
	defer db.Close()
	a, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer a.Close()
	exec := func(q string, count int64) error {
		r, err := a.ExecContext(ctx, q)
		if err != nil {
			return err
		}
		n, err := r.RowsAffected()
		if err != nil {
			return err
		}
		if n != count {
			return fmt.Errorf("%s affected %d want %d", q, n, count)
		}
		return callbackErr
	}
	show := func(name, want string) error {
		rows, err := a.QueryContext(ctx, "SHOW "+name)
		if err != nil {
			return err
		}
		defer rows.Close()
		types, err := rows.ColumnTypes()
		if err != nil {
			return err
		}
		if len(types) != 1 || types[0].Name() != name || types[0].DatabaseTypeName() != "TEXT" {
			return fmt.Errorf("SHOW metadata %v", types)
		}
		var got string
		if !rows.Next() {
			return errors.New("SHOW missing")
		}
		if err = rows.Scan(&got); err != nil {
			return err
		}
		if got != want || rows.Next() {
			return fmt.Errorf("SHOW %s %q want %q", name, got, want)
		}
		return rows.Err()
	}
	diagnostic := func(err error, code, message, hint string) error {
		var got *stuntgres.Error
		want := stuntgres.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: code, Message: message, Hint: hint}
		if !errors.As(err, &got) || *got != want {
			return fmt.Errorf("diagnostic %#v (%v), want %#v", got, err, want)
		}
		return nil
	}
	if err = exec("CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)", 0); err != nil {
		return err
	}
	if err = exec("INSERT INTO notice_rows VALUES(7,'kept')", 1); err != nil {
		return err
	}
	if len(notices) != 0 {
		return errors.New("fresh notice")
	}
	if err = exec("CREATE TABLE IF NOT EXISTS notice_rows(replaced BOOL)", 0); err != nil {
		return err
	}
	stmt, err := a.PrepareContext(ctx, "CREATE TABLE IF NOT EXISTS notice_rows(id INT,id TEXT)")
	if err != nil {
		return err
	}
	if len(notices) != 1 {
		stmt.Close()
		return errors.New("prepare emitted notice")
	}
	r, err := stmt.ExecContext(ctx)
	stmt.Close()
	if err != nil {
		return err
	}
	n, err := r.RowsAffected()
	if err != nil || n != 0 {
		return fmt.Errorf("prepared count %d %v", n, err)
	}
	want := stuntgres.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}
	if len(notices) != 2 || notices[0] != want || notices[1] != want {
		return fmt.Errorf("duplicate notices %#v", notices)
	}
	rows, err := a.QueryContext(ctx, "SELECT * FROM notice_rows ORDER BY id")
	if err != nil {
		return err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		rows.Close()
		return err
	}
	if len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
		rows.Close()
		return errors.New("definition changed")
	}
	var id int
	var note string
	if !rows.Next() {
		rows.Close()
		return errors.New("missing sentinel")
	}
	err = rows.Scan(&id, &note)
	more := rows.Next()
	rowErr := rows.Err()
	rows.Close()
	if err != nil || rowErr != nil || more || id != 7 || note != "kept" {
		return fmt.Errorf("sentinel %d %q %v %v", id, note, err, rowErr)
	}
	if err = exec("SET client_min_messages TO warning", 0); err != nil {
		return err
	}
	if err = exec("CREATE TABLE IF NOT EXISTS notice_rows(x INT)", 0); err != nil {
		return err
	}
	if len(notices) != 2 {
		return errors.New("filter failed")
	}
	_, err = a.ExecContext(ctx, "CREATE TABLE notice_rows(x INT)")
	if err = diagnostic(err, "42P07", `relation "notice_rows" already exists`, ""); err != nil {
		return err
	}
	if err = exec("SET client_min_messages TO debug", 0); err != nil {
		return err
	}
	if err = show("client_min_messages", "debug2"); err != nil {
		return err
	}
	if err = exec("SET client_min_messages TO DEFAULT", 0); err != nil {
		return err
	}
	if err = show("client_min_messages", "notice"); err != nil {
		return err
	}
	_, err = a.ExecContext(ctx, "SET client_min_messages TO bogus")
	if err = diagnostic(err, "22023", `invalid value for parameter "client_min_messages": "bogus"`, "Available values: debug5, debug4, debug3, debug2, debug1, log, notice, warning, error."); err != nil {
		return err
	}
	if err = show("client_min_messages", "notice"); err != nil {
		return err
	}
	original := strings.Repeat("a", 64)
	clipped := strings.Repeat("a", 63)
	if err = exec("SET application_name TO '"+original+"'", 0); err != nil {
		return err
	}
	if err = show("application_name", clipped); err != nil {
		return err
	}
	clipping := stuntgres.Error{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + original + `" will be truncated to "` + clipped + `"`}
	if len(notices) != 3 || notices[2] != clipping || notices[0] != want {
		return fmt.Errorf("clipping/ownership %#v", notices)
	}
	var peerThreshold string
	if err = peer.QueryRowContext(ctx, "SHOW client_min_messages").Scan(&peerThreshold); err != nil || peerThreshold != "notice" {
		return fmt.Errorf("peer threshold %q %v", peerThreshold, err)
	}
	if _, err = peer.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS notice_rows(other BOOL)"); err != nil {
		return err
	}
	if len(notices) != 3 {
		return errors.New("connector callback leaked")
	}
	if err = exec("SET client_min_messages TO warning", 0); err != nil {
		return err
	}
	if err = peer.QueryRowContext(ctx, "SHOW client_min_messages").Scan(&peerThreshold); err != nil || peerThreshold != "notice" {
		return fmt.Errorf("threshold isolation %q %v", peerThreshold, err)
	}
	if err = exec("SET client_min_messages TO DEFAULT", 0); err != nil {
		return err
	}
	canceled, stop := context.WithCancel(ctx)
	stop()
	_, err = a.ExecContext(canceled, "CREATE TABLE IF NOT EXISTS notice_rows(x INT)")
	if !errors.Is(err, context.Canceled) || len(notices) != 3 {
		return fmt.Errorf("cancellation %v", err)
	}
	var dump bytes.Buffer
	if err = stuntgres.Dump(ctx, db, &dump); err != nil {
		return err
	}
	if strings.Contains(dump.String(), "IF NOT EXISTS") {
		return errors.New("dump serialized notice syntax")
	}
	if err = stuntgres.Reset(ctx, db); err != nil {
		return err
	}
	if err = stuntgres.Restore(ctx, db, &dump); err != nil {
		return err
	}
	if err = peer.QueryRowContext(ctx, "SELECT id,note FROM notice_rows").Scan(&id, &note); err != nil || id != 7 || note != "kept" {
		return fmt.Errorf("restore %d %q %v", id, note, err)
	}
	if len(notices) != 3 {
		return errors.New("control callback")
	}
	if callbackErr != nil {
		return callbackErr
	}
	return nil
}
func TestNoticesSmoke(t *testing.T) {
	if err := noticesRun(); err != nil {
		t.Fatal(err)
	}
}
