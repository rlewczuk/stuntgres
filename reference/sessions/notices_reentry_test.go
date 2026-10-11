package reference_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// Return callback failures to the joined command owner rather than calling
// Fatal in a worker (which would prevent that worker publishing completion).
func noticesPeerState(ctx context.Context, peer *sql.Conn) error {
	rows, err := peer.QueryContext(ctx, "SELECT * FROM notice_rows ORDER BY id")
	if err != nil {
		return err
	}
	defer rows.Close()
	types, err := rows.ColumnTypes()
	if err != nil || len(types) != 2 || types[0].DatabaseTypeName() != "INT4" || types[1].DatabaseTypeName() != "TEXT" {
		return fmt.Errorf("callback row metadata %v: %v", types, err)
	}
	var id int
	var note string
	if !rows.Next() {
		return fmt.Errorf("callback missing committed row: %v", rows.Err())
	}
	if err := rows.Scan(&id, &note); err != nil || id != 7 || note != "kept" {
		return fmt.Errorf("callback row %d %q: %v", id, note, err)
	}
	if rows.Next() || rows.Err() != nil {
		return fmt.Errorf("callback row exhaustion: %v", rows.Err())
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, setting := range []struct{ name, want string }{{"application_name", ""}, {"client_min_messages", "notice"}} {
		rows, err := peer.QueryContext(ctx, "SHOW "+setting.name)
		if err != nil {
			return err
		}
		types, err := rows.ColumnTypes()
		if err != nil || len(types) != 1 || types[0].Name() != setting.name || types[0].DatabaseTypeName() != "TEXT" {
			rows.Close()
			return fmt.Errorf("callback SHOW metadata %v: %v", types, err)
		}
		var got string
		if !rows.Next() {
			rows.Close()
			return fmt.Errorf("callback SHOW missing %s", setting.name)
		}
		err = rows.Scan(&got)
		if err != nil || got != setting.want || rows.Next() || rows.Err() != nil {
			rows.Close()
			return fmt.Errorf("callback SHOW %s=%q: %v", setting.name, got, err)
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

func TestReferenceNoticesReentrant(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			var peerNotices, notices, copies []fixture.Diagnostic
			joined := true
			ownership := fixture.NoticeReferenceOption{Joined: func() bool { return joined }}
			_, peer, _ := fixture.NoticeReferencePool(t, p, backend.PG, &peerNotices, ownership)
			callbackErrors := make(chan error, 2)
			_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices, fixture.NoticeReferenceOption{
				Joined: ownership.Joined,
				Handler: func(n fixture.Diagnostic) {
					callbackErrors <- noticesPeerState(ctx, peer)
					copies = append(copies, n)
					n.Message = "changed"
					n.Detail = "mutated"
				},
			})
			fixture.RunStep(t, ctx, c, record, fixture.Exec("CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)", 0))
			fixture.RunStep(t, ctx, c, record, fixture.Exec("INSERT INTO notice_rows VALUES(7,'kept')", 1))
			returned := make(chan error, 1)
			defer func() {
				cancel()
				if !joined {
					select {
					case <-returned:
						joined = true
					default:
					}
				}
			}()
			joined = false
			go func() {
				for range 2 {
					result, err := c.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS notice_rows(x BOOL)")
					if err != nil {
						returned <- err
						return
					}
					if count, err := result.RowsAffected(); err != nil || count != 0 {
						returned <- fmt.Errorf("command count %d: %v", count, err)
						return
					}
				}
				returned <- nil
			}()
			select {
			case err := <-returned:
				joined = true
				if err != nil {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("peer callback failed to join", ctx.Err())
			}
			if len(callbackErrors) != 2 {
				t.Fatal("missing callbacks", len(callbackErrors))
			}
			for range 2 {
				if err := <-callbackErrors; err != nil {
					t.Fatal(err)
				}
			}
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}
			if !reflect.DeepEqual(notices, []fixture.Diagnostic{want, want}) || !reflect.DeepEqual(copies, []fixture.Diagnostic{want, want}) || len(peerNotices) != 0 {
				t.Fatal(notices, copies, peerNotices)
			}
		})
	}
}

func TestReferenceNoticesPhysicalCloseReentry(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			var notices []fixture.Diagnostic
			_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices)
			fixture.RunStep(t, p.Ctx, c, record, fixture.Exec("CREATE TABLE notice_rows(id INT NOT NULL,note TEXT)", 0))
			fixture.RunStep(t, p.Ctx, c, record, fixture.Exec("INSERT INTO notice_rows VALUES(7,'kept')", 1))
			if backend.PG {
				// pq transport is not reentrant. Its real peer notice/committed
				// SQL witness complements the native lease-release Close phase.
				fixture.RunStep(t, p.Ctx, c, record, fixture.Exec("CREATE TABLE IF NOT EXISTS notice_rows(x BOOL)", 0))
				noticesRows(t, p.Ctx, c, record)
				want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42P07", Message: `relation "notice_rows" already exists, skipping`}
				if len(notices) != 1 || notices[0] != want {
					t.Fatal(notices)
				}
				return
			}
			ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
			defer cancel()
			var physical driver.Conn
			var closeErr error
			physical, err := p.NativeConnector.WithNoticeHandler(func(stuntgres.Error) { closeErr = physical.Close() }).Connect(ctx)
			if err != nil {
				t.Fatal(err)
			}
			joined := false
			defer func() {
				cancel()
				if joined {
					physical.Close()
				} else {
					go physical.Close()
				}
			}()
			returned := make(chan error, 1)
			go func() {
				_, err := physical.(driver.ExecerContext).ExecContext(ctx, "CREATE TABLE IF NOT EXISTS notice_rows(x BOOL)", nil)
				returned <- err
			}()
			select {
			case err := <-returned:
				joined = true
				if err != nil || closeErr != nil {
					t.Fatal("raw physical Close reentry", err, closeErr)
				}
			case <-ctx.Done():
				t.Fatal("raw physical Close failed to join", ctx.Err())
			}
		})
	}
}

func TestReferenceNoticesSynchronous(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			released, joined := false, true
			var notices []fixture.Diagnostic
			_, c, record := fixture.NoticeReferencePool(t, p, backend.PG, &notices, fixture.NoticeReferenceOption{
				Handler: func(fixture.Diagnostic) { close(entered); <-release },
				Joined:  func() bool { return joined },
			})
			deadline, stop := context.WithTimeout(p.Ctx, 5*time.Second)
			ctx, cancel := context.WithCancel(deadline)
			returned := make(chan error, 1)
			defer func() {
				cancel()
				stop()
				if !released {
					close(release)
				}
				if !joined {
					select {
					case <-returned:
						joined = true
					default:
					}
				}
			}()
			joined = false
			go func() {
				_, err := c.ExecContext(ctx, "SET application_name TO '"+strings.Repeat("a", 64)+"'")
				returned <- err
			}()
			select {
			case <-entered:
			case <-deadline.Done():
				t.Fatal("callback did not enter", deadline.Err())
			}
			select {
			case err := <-returned:
				joined = true
				t.Fatal("returned before callback", err)
			default:
			}
			// Native dispatch happens after statement completion, so callback-
			// time cancellation must not replace success or invalidate settings.
			// pq's Go-context watcher races transport retirement even after a
			// successful command. Its counterpart proves synchronous delivery
			// and healthy continuation without canceling that transport;
			// admitted PostgreSQL cancellation is covered separately.
			if !backend.PG {
				cancel()
			}
			close(release)
			released = true
			select {
			case err := <-returned:
				joined = true
				if err != nil {
					t.Fatal("execution result replaced", err)
				}
			case <-deadline.Done():
				t.Fatal("callback command failed to join", deadline.Err())
			}
			want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: `identifier "` + strings.Repeat("a", 64) + `" will be truncated to "` + strings.Repeat("a", 63) + `"`}
			if len(notices) != 1 || notices[0] != want {
				t.Fatal(notices)
			}
			noticesShow(t, p.Ctx, c, record, "application_name", strings.Repeat("a", 63))
		})
	}
}
