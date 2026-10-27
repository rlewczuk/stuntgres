package fixture

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"fmt"
	"net/url"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"
	"stuntgres"
)

// Shared direct/prepared execution uses only existing public APIs.
func RoleAttributesRun(t *testing.T, p *Pair, b Backend, c Executor, prepared bool, s Step) {
	t.Helper()
	s.Prepared = prepared || s.Prepared
	RunStep(t, p.Ctx, c, b.Recorder, s)
}

func RoleAttributesPhysical(t *testing.T, p *Pair, b Backend, name string) (driver.Conn, error) {
	t.Helper()
	var connector driver.Connector
	var err error
	if b.PG {
		u, e := url.Parse(DSN)
		if e != nil {
			t.Fatal(e)
		}
		u.User = url.UserPassword(name, "role-attributes-fixture")
		connector, err = pq.NewConnector(u.String())
	} else {
		connector, err = p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(p.NativeDSN, name)
	}
	if err != nil {
		t.Fatal(err)
	}
	conn, err := connector.Connect(p.Ctx)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func RoleAttributesPool(t *testing.T, p *Pair, b Backend, name string) *sql.DB {
	t.Helper()
	var connector driver.Connector
	var err error
	if b.PG {
		u, err := url.Parse(DSN)
		if err != nil {
			t.Fatal(err)
		}
		u.User = url.UserPassword(name, "role-attributes-fixture")
		connector, err = pq.NewConnector(u.String())
		if err != nil {
			t.Fatal(err)
		}
	} else {
		connector, err = p.NativeConnector.Driver().(*stuntgres.Driver).OpenConnectorAs(p.NativeDSN, name)
		if err != nil {
			t.Fatal(err)
		}
	}
	wrapped := sqlmw.Driver(NoticeReferenceDriver{connector}, Interceptor{})
	configured, err := wrapped.(driver.DriverContext).OpenConnector("")
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(configured)
	t.Cleanup(func() { db.Close() })
	return db
}
func RoleTransactionBegin(t *testing.T, p *Pair, b Backend, c *sql.Conn, mode string) (Executor, func(bool)) {
	t.Helper()
	if mode == "go" {
		tx, err := c.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { tx.Rollback() })
		return tx, func(commit bool) {
			var err error
			if commit {
				err = tx.Commit()
			} else {
				err = tx.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if mode == "sql" {
		RunStep(t, p.Ctx, c, b.Recorder, Exec("BEGIN", 0))
	}
	return c, func(commit bool) {
		if mode == "autocommit" {
			return
		}
		q := "ROLLBACK"
		if commit {
			q = "COMMIT"
		}
		RunStep(t, p.Ctx, c, b.Recorder, Exec(q, 0))
	}
}

func RoleTransactionStep(t *testing.T, p *Pair, b Backend, e Executor, held bool, s Step) {
	t.Helper()
	if !held {
		RunStep(t, p.Ctx, e, b.Recorder, s)
		return
	}
	stmt, err := e.PrepareContext(p.Ctx, s.Query)
	if err != nil {
		t.Fatal("held Prepare", err)
	}
	defer stmt.Close()
	RunStep(t, p.Ctx, HeldStatement{Executor: e, Statement: stmt, SQL: s.Query}, b.Recorder, s)
}

func RoleTransactionIdentity(session, current string) Step {
	return Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{session, current}})
}

func RoleTransactionExists(name string, present bool) Step {
	var count int64
	if present {
		count = 1
	}
	return Select("SELECT count(*) AS n FROM pg_catalog.pg_roles WHERE rolname='"+name+"'", []string{"n"}, []string{"INT8"}, [][]any{{count}})
}
func RoleTransactionValidOID(t *testing.T, text string) {
	t.Helper()
	oid, err := strconv.ParseUint(text, 10, 32)
	if err != nil || oid == 0 {
		t.Fatal("invalid role identity", text, err)
	}
}
func RoleTransactionConn(t *testing.T, p *Pair, b Backend) (*sql.Conn, int) {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	t.Cleanup(func() { c.ExecContext(p.Ctx, "ROLLBACK") })
	return c, RoleTransactionPID(t, p, b, c)
}

func RoleTransactionPID(t *testing.T, p *Pair, b Backend, c *sql.Conn) int {
	t.Helper()
	if b.PG {
		return LockPID(t, p.Ctx, c)
	}
	RunStep(t, p.Ctx, c, b.Recorder, Exec("CREATE TEMP SEQUENCE role_tx_physical_identity", 0))
	var name string
	if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	text, ok := strings.CutPrefix(name, "pg_temp_")
	pid, err := strconv.ParseUint(text, 10, 31)
	if !ok || err != nil || pid == 0 {
		t.Fatal("physical identity", name, err)
	}
	return int(pid)
}

func RoleTransactionWait(t *testing.T, p *Pair, b Backend, waiter, holder int, completed <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
	defer cancel()
	for {
		var blocked bool
		if err := b.DB.QueryRowContext(ctx, "SELECT $1=ANY(pg_blocking_pids($2))", holder, waiter).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked {
			return
		}
		select {
		case <-completed:
			t.Fatal("operation completed before the required role-lock wait")
		default:
		}
		if err := ctx.Err(); err != nil {
			t.Fatal(err)
		}
		runtime.Gosched()
	}
}

// Expected keys come from RFC5802's equations and explicitly specified prepared
// bytes, not a native password helper or backend comparison.
func RolePasswordKeys(t *testing.T, password string, salt []byte, iterations int) ([]byte, []byte) {
	t.Helper()
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, salted)
	mac.Write([]byte("Client Key"))
	stored := sha256.Sum256(mac.Sum(nil))
	mac = hmac.New(sha256.New, salted)
	mac.Write([]byte("Server Key"))
	return stored[:], mac.Sum(nil)
}

func RolePasswordEncoded(t *testing.T, password string, salt []byte, iterations int) string {
	t.Helper()
	stored, server := RolePasswordKeys(t, password, salt, iterations)
	return fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iterations, base64.StdEncoding.EncodeToString(salt), base64.StdEncoding.EncodeToString(stored), base64.StdEncoding.EncodeToString(server))
}

func RoleScenarioNameScan(pg bool) reflect.Type {
	if pg {
		return reflect.TypeFor[any]()
	}
	return reflect.TypeFor[string]()
}

func RoleScenarioCommand(t *testing.T, c *sql.Conn, q string, count int64, prepared bool) {
	t.Helper()
	var r sql.Result
	var err error
	if prepared {
		s, e := c.PrepareContext(context.Background(), q)
		if e != nil {
			t.Fatal(q, e)
		}
		defer s.Close()
		r, err = s.ExecContext(context.Background())
	} else {
		r, err = c.ExecContext(context.Background(), q)
	}
	if err != nil {
		t.Fatal(q, err)
	}
	if n, e := r.RowsAffected(); e != nil || n != count {
		t.Fatal(q, n, e)
	}
	if _, e := r.LastInsertId(); e == nil {
		t.Fatal("LastInsertId")
	}
}

func RoleScenarioIdentity(t *testing.T, c *sql.Conn, want string, held *sql.Stmt, pg bool) {
	t.Helper()
	var rows *sql.Rows
	var err error
	if held == nil {
		rows, err = c.QueryContext(context.Background(), "SELECT session_user,current_user,session_user")
	} else {
		rows, err = held.QueryContext(context.Background())
	}
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil || !reflect.DeepEqual(columns, []string{"session_user", "current_user", "session_user"}) {
		t.Fatal(columns, err)
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for _, typ := range types {
		if typ.DatabaseTypeName() != "NAME" || typ.ScanType() != RoleScenarioNameScan(pg) {
			t.Fatal(typ)
		}
	}
	if !rows.Next() {
		t.Fatal(rows.Err())
	}
	var a, b, cName string
	if err := rows.Scan(&a, &b, &cName); err != nil || a != want || b != want || cName != want {
		t.Fatal(a, b, cName, err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal(rows.Err())
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
}
