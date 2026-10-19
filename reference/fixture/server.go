package fixture

import (
	"bufio"
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/ngrok/sqlmw"
)

const (
	ServerUser           = "stuntgres"
	ServerPassword       = "server-contract-password"
	ServerDatabase       = "stuntgres_reference"
	ServerSecondDatabase = "server_second"
	ServerGuest          = "server_guest"
)

var serverBuild struct {
	sync.Once
	binary string
	err    error
}

// The salt and password are constant, so every fixture shares one verifier.
var serverUsers struct {
	sync.Once
	text string
	err  error
}

func serverUserfile() (string, error) {
	serverUsers.Do(func() {
		salt := []byte("server-test-salt") // Exactly sixteen deterministic bytes; this is test-only material.
		key, err := pbkdf2.Key(sha256.New, ServerPassword, salt, serverKDFIterations, 32)
		if err != nil {
			serverUsers.err = err
			return
		}
		hash := "$pbkdf2-sha256$i=" + strconv.Itoa(serverKDFIterations) + "$" + base64.RawStdEncoding.EncodeToString(salt) + "$" + base64.RawStdEncoding.EncodeToString(key)
		serverUsers.text = ServerUser + ":" + hash + ":" + ServerDatabase + "," + ServerSecondDatabase + "," + ServerUser + "\n" + ServerGuest + ":" + hash + ":" + ServerDatabase + "\n"
	})
	return serverUsers.text, serverUsers.err
}

func cleanupServerBuild() error {
	if serverBuild.binary == "" {
		return nil
	}
	return os.RemoveAll(filepath.Dir(serverBuild.binary))
}

// ServerFixture is opt-in: existing embedded scenarios never build or launch a server.
type ServerFixture struct {
	Ctx                                                      context.Context
	Address, DSN, Binary, Userfile, Dumpdir, Logdir, PIDFile string
	t                                                        *testing.T
	backends                                                 []Backend
	pools                                                    []*sql.DB
	cmd                                                      *exec.Cmd
	done                                                     chan error
	mu                                                       sync.Mutex
	output                                                   strings.Builder
	events                                                   []map[string]any
	changed                                                  chan struct{}
	readers                                                  sync.WaitGroup
}

func NewServerFixture(t *testing.T) *ServerFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	dir := t.TempDir()
	f := &ServerFixture{Ctx: ctx, t: t, Userfile: filepath.Join(dir, "users"), Dumpdir: filepath.Join(dir, "dumps"), Logdir: filepath.Join(dir, "logs"), PIDFile: filepath.Join(dir, "server.pid"), changed: make(chan struct{})}
	t.Cleanup(func() {
		f.closePools()
		if f.cmd != nil {
			f.Stop(t)
		}
		cancel()
	})
	if *Mode {
		if err := EnsureStarted(); err != nil {
			t.Fatal(err)
		}
		f.resetReference(t)
		for _, q := range []string{
			"CREATE DATABASE server_second OWNER stuntgres", "CREATE DATABASE stuntgres OWNER stuntgres",
			"CREATE ROLE server_guest LOGIN PASSWORD 'server-contract-password'",
			"REVOKE CONNECT ON DATABASE server_second FROM PUBLIC",
			"GRANT CONNECT ON DATABASE stuntgres_reference TO server_guest",
		} {
			if _, err := Admin.ExecContext(ctx, q); err != nil {
				t.Fatalf("server oracle provision %s: %v", q, err)
			}
		}
		t.Cleanup(func() {
			f.closePools()
			for _, q := range []string{"DROP DATABASE server_second WITH (FORCE)", "DROP DATABASE stuntgres WITH (FORCE)", "DROP OWNED BY server_guest CASCADE", "DROP ROLE server_guest"} {
				c, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				_, err := Admin.ExecContext(c, q)
				cancel()
				if err != nil {
					t.Errorf("server oracle cleanup %s: %v", q, err)
				}
			}
			f.resetReference(t)
		})
		rec := &Recorder{RawBackend: RawPostgres}
		f.backends = append(f.backends, Backend{Name: "postgres", DB: f.openPool(t, DSN, rec), Recorder: rec, PG: true})
	}
	serverBuild.Do(func() {
		root, err := os.Getwd()
		if err != nil {
			serverBuild.err = err
			return
		}
		for {
			if _, err := os.Stat(filepath.Join(root, "go.mod")); err == nil {
				break
			}
			next := filepath.Dir(root)
			if next == root {
				serverBuild.err = errors.New("repository go.mod not found")
				return
			}
			root = next
		}
		builddir, err := os.MkdirTemp("", "stuntgres-server-contract-")
		if err != nil {
			serverBuild.err = err
			return
		}
		serverBuild.binary = filepath.Join(builddir, "stuntgres")
		buildArgs := []string{"build"}
		if serverRaceBuild {
			buildArgs = append(buildArgs, "-race")
		}
		if serverKDFTag != "" {
			buildArgs = append(buildArgs, "-tags", serverKDFTag)
		}
		buildArgs = append(buildArgs, "-o", serverBuild.binary, "./cmd/stuntgres")
		c := exec.CommandContext(ctx, "go", buildArgs...)
		c.Dir = root
		out, err := c.CombinedOutput()
		if err != nil {
			serverBuild.err = fmt.Errorf("build real server command: %w\n%s", err, out)
		}
	})
	if serverBuild.err != nil {
		t.Fatal(serverBuild.err)
	}
	f.Binary = serverBuild.binary
	users, err := serverUserfile()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.Userfile, []byte(users), 0600); err != nil {
		t.Fatal(err)
	}
	f.start(t, false)
	rec := &Recorder{RawBackend: RawPostgres}
	native := Backend{Name: "server", DB: f.openPool(t, f.DSN, rec), Recorder: rec}
	f.backends = append([]Backend{native}, f.backends...)
	return f
}

func (f *ServerFixture) resetReference(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, err := Admin.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, q := range []string{"ROLLBACK", "DISCARD ALL", "DROP SCHEMA public CASCADE; CREATE SCHEMA public;"} {
		if _, err := c.ExecContext(ctx, q); err != nil {
			t.Fatalf("server reference reset: %v", err)
		}
	}
}

func (f *ServerFixture) openPool(t *testing.T, dsn string, rec *Recorder) *sql.DB {
	t.Helper()
	base, err := pq.NewConnector(dsn)
	if err != nil {
		t.Fatal(err)
	}
	var connector driver.Connector = base
	if rec != nil {
		wrapped := sqlmw.Driver(NoticeReferenceDriver{connector}, Interceptor{})
		connector, err = wrapped.(driver.DriverContext).OpenConnector("")
		if err != nil {
			t.Fatal(err)
		}
	}
	db := sql.OpenDB(connector)
	f.pools = append(f.pools, db)
	if err := db.PingContext(f.Ctx); err != nil {
		t.Fatal(err)
	}
	return db
}
func (f *ServerFixture) closePools() {
	for _, db := range f.pools {
		db.Close()
	}
	f.pools = nil
}
func (f *ServerFixture) Backends() []Backend { return append([]Backend(nil), f.backends...) }
func (f *ServerFixture) NativeBackend() (Backend, bool) {
	for _, b := range f.backends {
		if !b.PG {
			return b, true
		}
	}
	return Backend{}, false
}
func (f *ServerFixture) DSNFor(b Backend) string {
	if b.PG {
		return DSN
	}
	return f.DSN
}
func (f *ServerFixture) UserFor(b Backend) string { return ServerUser }
func (f *ServerFixture) PasswordFor(b Backend) string {
	if b.PG {
		return "stuntgres"
	}
	return ServerPassword
}
func (f *ServerFixture) DatabaseFor(b Backend) string { return ServerDatabase }
func (f *ServerFixture) AddressFor(b Backend) string {
	u, err := url.Parse(f.DSNFor(b))
	if err != nil {
		f.t.Fatal(err)
	}
	return u.Host
}
func (f *ServerFixture) AuthDSN(t *testing.T, b Backend, user, password, database string) string {
	t.Helper()
	u, err := url.Parse(f.DSNFor(b))
	if err != nil {
		t.Fatal(err)
	}
	u.User = url.UserPassword(user, password)
	u.Path = "/" + database
	return u.String()
}
func (f *ServerFixture) DSNForDatabase(b Backend, database string) string {
	return f.AuthDSN(f.t, b, f.UserFor(b), f.PasswordFor(b), database)
}
func (f *ServerFixture) RawPool(t *testing.T, b Backend) *sql.DB {
	return f.openPool(t, f.DSNFor(b), nil)
}
func (f *ServerFixture) NativeDB(t *testing.T, database string) *sql.DB {
	b, ok := f.NativeBackend()
	if !ok {
		t.Fatal("native process required")
	}
	return f.openPool(t, f.DSNForDatabase(b, database), nil)
}
func (f *ServerFixture) ReferenceReplayDSN() string { return DSN }

func (f *ServerFixture) start(t *testing.T, restore bool) {
	t.Helper()
	args := []string{"serve", "-l", "127.0.0.1:0", "-u", f.Userfile, "-d", f.Dumpdir, "-L", f.Logdir, "-p", f.PIDFile}
	if restore {
		args = append(args, "-r")
	}
	f.cmd = exec.Command(f.Binary, args...)
	stdout, err := f.cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := f.cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.events = nil
	f.mu.Unlock()
	if err := f.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	for _, r := range []interface{ Read([]byte) (int, error) }{stdout, stderr} {
		f.readers.Add(1)
		go func(r interface{ Read([]byte) (int, error) }) {
			defer f.readers.Done()
			s := bufio.NewScanner(r)
			s.Buffer(make([]byte, 4096), 16<<20)
			for s.Scan() {
				line := s.Text()
				var e map[string]any
				_ = json.Unmarshal([]byte(line), &e)
				f.mu.Lock()
				f.output.WriteString(line + "\n")
				if e != nil {
					f.events = append(f.events, e)
				}
				close(f.changed)
				f.changed = make(chan struct{})
				f.mu.Unlock()
			}
		}(r)
	}
	f.done = make(chan error, 1)
	go func() { err := f.cmd.Wait(); f.readers.Wait(); f.done <- err }()
	e := f.WaitEvent(t, func(e map[string]any) bool {
		for _, key := range []string{"address", "listen"} {
			if s, ok := e[key].(string); ok {
				_, _, err := net.SplitHostPort(s)
				if err == nil {
					return true
				}
			}
		}
		return false
	})
	for _, key := range []string{"address", "listen"} {
		if s, ok := e[key].(string); ok {
			f.Address = s
			break
		}
	}
	u := url.URL{Scheme: "postgres", Host: f.Address, Path: "/" + ServerDatabase, User: url.UserPassword(ServerUser, ServerPassword)}
	q := u.Query()
	q.Set("sslmode", "disable")
	u.RawQuery = q.Encode()
	f.DSN = u.String()
}
func (f *ServerFixture) Output() string { f.mu.Lock(); defer f.mu.Unlock(); return f.output.String() }
func (f *ServerFixture) WaitEvent(t *testing.T, predicate func(map[string]any) bool) map[string]any {
	t.Helper()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	for {
		f.mu.Lock()
		for _, e := range f.events {
			if predicate(e) {
				f.mu.Unlock()
				return e
			}
		}
		changed := f.changed
		f.mu.Unlock()
		select {
		case <-changed:
		case err := <-f.done:
			f.done <- err
			t.Fatalf("server exited before expected event: %v\n%s", err, f.Output())
		case <-timer.C:
			t.Fatalf("server event deadline\n%s", f.Output())
		case <-f.Ctx.Done():
			t.Fatal(f.Ctx.Err())
		}
	}
}
func (f *ServerFixture) Stop(t *testing.T) {
	t.Helper()
	if f.cmd == nil {
		return
	}
	f.closePools()
	if err := f.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("server terminate: %v", err)
	}
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case err := <-f.done:
		if err != nil {
			t.Errorf("server shutdown: %v\n%s", err, f.Output())
		}
	case <-timer.C:
		f.cmd.Process.Kill()
		<-f.done
		t.Errorf("server failed to join before deadline\n%s", f.Output())
	}
	f.cmd = nil
}
func (f *ServerFixture) Restart(t *testing.T, restore bool) {
	t.Helper()
	f.Stop(t)
	f.start(t, restore)
	for i, b := range f.backends {
		if !b.PG {
			f.backends[i].DB = f.openPool(t, f.DSN, b.Recorder)
		} else {
			f.backends[i].DB = f.openPool(t, DSN, b.Recorder)
		}
	}
}
func (f *ServerFixture) RunCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	// A daemon inherits stdout: a pipe would keep CombinedOutput waiting after its parent exits.
	path := filepath.Join(t.TempDir(), "cli-output")
	log, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0600)
	if err != nil {
		return "", err
	}
	c := exec.CommandContext(ctx, f.Binary, args...)
	c.Stdout = log
	c.Stderr = log
	err = c.Run()
	err = errors.Join(err, log.Close())
	out, readErr := os.ReadFile(path)
	return string(out), errors.Join(err, readErr)
}

// PSQL requires the actual client; a missing binary is a failed prerequisite.
func (f *ServerFixture) PSQL(t *testing.T, b Backend, source string, extra ...string) (string, error) {
	t.Helper()
	binary, err := exec.LookPath("psql")
	if err != nil {
		t.Fatal("psql prerequisite: ", err)
	}
	u, err := url.Parse(f.DSNFor(b))
	if err != nil {
		t.Fatal(err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		t.Fatal(err)
	}
	escape := func(s string) string { return strings.NewReplacer("\\", "\\\\", ":", "\\:").Replace(s) }
	pass := filepath.Join(t.TempDir(), "pgpass")
	line := escape(host) + ":" + escape(port) + ":" + escape(strings.TrimPrefix(u.Path, "/")) + ":" + escape(f.UserFor(b)) + ":" + escape(f.PasswordFor(b)) + "\n"
	if err := os.WriteFile(pass, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	u.User = url.User(f.UserFor(b))
	args := append([]string{"-X", "-w", "-v", "ON_ERROR_STOP=1", "-At", "-d", u.String()}, extra...)
	c := exec.CommandContext(f.Ctx, binary, args...)
	c.Env = append(os.Environ(), "PGPASSFILE="+pass)
	c.Stdin = strings.NewReader(source)
	out, err := c.CombinedOutput()
	return string(out), err
}
