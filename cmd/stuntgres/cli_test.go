package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func TestServerCLI(t *testing.T) {
	clearConfigEnvironment(t)
	t.Run("aliases_and_reexec", func(t *testing.T) {
		command, err := parseCLI([]string{"start", "-l", "5435", "--listen", "[::1]:0", "-u", "users", "-L", "logs", "-d", "dumps", "-p", "pid", "-r", "-v", "--querylog"}, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		o := command.options
		if o.listen != "[::1]:0" || !o.background || !o.restore || !o.verbose || !o.querylog {
			t.Fatal(o)
		}
		assertAbsolutePaths(t, o)
		assertReexec(t, o)
	})
	t.Run("password_grants", func(t *testing.T) {
		c, err := parseCLI([]string{"passwd", "-u", "users", "--databases", "CaseSensitive,db--part", "Alice"}, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if c.username != "Alice" || !c.grantsSet || !reflect.DeepEqual(c.databases, []string{"CaseSensitive", "db--part"}) || !filepath.IsAbs(c.options.userfile) {
			t.Fatal(c)
		}
		c, err = parseCLI([]string{"passwd", "-u", "users", "Alice"}, &bytes.Buffer{})
		if err != nil || c.grantsSet {
			t.Fatalf("preservation%+v %v", c, err)
		}
	})
	t.Run("defaults", func(t *testing.T) {
		c, err := parseCLI([]string{"serve", "-u", "users"}, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		cwd, _ := os.Getwd()
		if c.options.listen != "127.0.0.1:5434" || c.options.pidfile != filepath.Join(cwd, "stuntgres.pid") || c.options.background || c.options.dumpdir != "" {
			t.Fatalf("%+v", c.options)
		}
		c, err = parseCLI([]string{"stop"}, &bytes.Buffer{})
		if err != nil || c.command != "stop" || c.options.pidfile != filepath.Join(cwd, "stuntgres.pid") {
			t.Fatalf("%+v %v", c, err)
		}
	})
	for _, args := range [][]string{
		{}, {"unknown"}, {"serve"}, {"serve", "-u", "users", "-r"}, {"serve", "-u", "users", "-q"}, {"serve", "-u", "users", "extra"},
		{"serve", "-u", "users", "--unsupported"}, {"serve", "-u", "users", "-l", "::1:5434"},
		{"serve", "-u", "users", "-l", "65536"}, {"serve", "-u", "users", "-l", "-1"},
		{"serve", "-u", "users", "-l", "localhost:postgres"}, {"stop", "extra"}, {"dump", "-u", "users"},
		{"passwd", "Alice"}, {"passwd", "-u", "users", "Alice", "Bob"},
		{"passwd", "-u", "users", "--databases", "", "Alice"}, {"passwd", "-u", "users", "--databases", "db,db", "Alice"},
		{"passwd", "-u", "users", "--databases", "../db", "Alice"},
		{"serve", "-u", "users", "--config", "missing.yaml"},
	} {
		t.Run("reject_"+joinCLIName(args), func(t *testing.T) {
			if c, err := parseCLI(args, &bytes.Buffer{}); err == nil || errors.Is(err, errHelp) {
				t.Fatalf("accepted%v: %+v %v", args, c, err)
			}
		})
	}
	address, err := normalizeListen("0")
	if err != nil || address != "127.0.0.1:0" {
		t.Fatalf("bare port%q %v", address, err)
	}
}

func TestServerCLIHelp(t *testing.T) {
	clearConfigEnvironment(t)
	for _, tc := range []struct {
		args []string
		want []string
	}{
		{[]string{"--help"}, []string{"WARNING: cleartext password", "STUNTGRES_LISTEN", "serve", "start", "dump", "stop", "passwd"}},
		{[]string{"-h"}, []string{"WARNING: cleartext password"}},
		{[]string{"serve", "--help"}, []string{"-l, --listen", "-L, --logdir", "-q, --querylog", "--config", "127.0.0.1:5434"}},
		{[]string{"help", "passwd"}, []string{"--databases", "-u, --userfile"}},
	} {
		var out bytes.Buffer
		if _, err := parseCLI(tc.args, &out); !errors.Is(err, errHelp) {
			t.Fatalf("help%v: %v", tc.args, err)
		}
		for _, want := range tc.want {
			if !strings.Contains(out.String(), want) {
				t.Fatalf("help%v lacks %q:\n%s", tc.args, want, out.String())
			}
		}
	}
	var out bytes.Buffer
	if _, err := parseCLI(nil, &out); err == nil || !strings.Contains(out.String(), "passwd") {
		t.Fatalf("empty command: %v\n%s", err, out.String())
	}
}

func TestServerCLIConfigSources(t *testing.T) {
	clearConfigEnvironment(t)
	dir := t.TempDir()
	yaml := writeCLIConfig(t, dir, "server.yaml", "listen: 5440\nuserfile: cfg-users\ndumpdir: cfg-dumps\nrestore: true\nlogdir: cfg-logs\n")
	toml := writeCLIConfig(t, dir, "server.toml", "listen = \"0\"\nuserfile = \"toml-users\"\nquerylog = true\nlogdir = \"toml-logs\"\n")
	parse := func(args ...string) serveOptions {
		t.Helper()
		c, err := parseCLI(args, &bytes.Buffer{})
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		assertAbsolutePaths(t, c.options)
		return c.options
	}
	o := parse("serve", "--config", yaml)
	cwd, _ := os.Getwd()
	if o.listen != "127.0.0.1:5440" || o.userfile != filepath.Join(cwd, "cfg-users") || !o.restore || o.querylog {
		t.Fatalf("yaml %+v", o)
	}
	assertReexec(t, o)
	if o = parse("serve", "--config", toml); o.listen != "127.0.0.1:0" || !o.querylog || o.userfile != filepath.Join(cwd, "toml-users") {
		t.Fatalf("toml %+v", o)
	}
	t.Setenv("STUNTGRES_LISTEN", "[::1]:6000")
	t.Setenv("STUNTGRES_USERFILE", "env-users")
	t.Setenv("STUNTGRES_VERBOSE", "true")
	if o = parse("serve", "--config", yaml); o.listen != "[::1]:6000" || o.userfile != filepath.Join(cwd, "env-users") || !o.verbose || !o.restore {
		t.Fatalf("env over config %+v", o)
	}
	if o = parse("serve", "--config", yaml, "-l", "7000", "--userfile", "flag-users"); o.listen != "127.0.0.1:7000" || o.userfile != filepath.Join(cwd, "flag-users") {
		t.Fatalf("flag over env %+v", o)
	}
	t.Setenv("STUNTGRES_PIDFILE", "env.pid")
	c, err := parseCLI([]string{"dump", "--config", yaml}, &bytes.Buffer{})
	if err != nil || c.options.pidfile != filepath.Join(cwd, "env.pid") {
		t.Fatalf("dump %+v %v", c, err)
	}
	c, err = parseCLI([]string{"passwd", "Alice"}, &bytes.Buffer{})
	if err != nil || c.options.userfile != filepath.Join(cwd, "env-users") || c.grantsSet {
		t.Fatalf("passwd %+v %v", c, err)
	}
}

func TestServerCLIConfigRejects(t *testing.T) {
	clearConfigEnvironment(t)
	dir := t.TempDir()
	for name, body := range map[string]string{
		"typo.yaml":      "userfile: users\nlisten_addr: 5434\n",
		"grants.yaml":    "userfile: users\ndatabases: app\n",
		"nested.yaml":    "userfile: users\nlisten:\n  port: 5434\n",
		"badbool.toml":   "userfile = \"users\"\nrestore = \"maybe\"\ndumpdir = \"d\"\n",
		"badtype.json":   `{"userfile": "users", "verbose": 1}`,
		"malformed.json": `{"userfile": `,
		"unknown.ini2":   "userfile=users\n",
	} {
		path := writeCLIConfig(t, dir, name, body)
		if c, err := parseCLI([]string{"serve", "--config", path}, &bytes.Buffer{}); err == nil {
			t.Fatalf("%s accepted %+v", name, c)
		}
	}
	t.Setenv("STUNTGRES_RESTORE", "maybe")
	if _, err := parseCLI([]string{"serve", "-u", "users", "-d", "dumps"}, &bytes.Buffer{}); err == nil {
		t.Fatal("invalid boolean environment accepted")
	}
}

func TestServerCLIChildEnvironment(t *testing.T) {
	got := childEnvironment([]string{"PATH=/bin", "STUNTGRES_LISTEN=1", "STUNTGRES_RESTORE=true", "STUNTGRES_READY_FD=3", "STUNTGRES_OTHER=x", "STUNTGRES_listen=y", "HOME=/h"})
	want := []string{"PATH=/bin", "STUNTGRES_OTHER=x", "STUNTGRES_listen=y", "HOME=/h"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// clearConfigEnvironment keeps ambient STUNTGRES_* variables out of a test;
// empty values are ignored by the configuration resolver.
func clearConfigEnvironment(t *testing.T) {
	for _, key := range serveKeys {
		t.Setenv(envPrefix+"_"+strings.ToUpper(envKeys.Replace(key)), "")
	}
}

func writeCLIConfig(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertAbsolutePaths(t *testing.T, o serveOptions) {
	t.Helper()
	for _, path := range []string{o.userfile, o.logdir, o.dumpdir, o.pidfile} {
		if path != "" && !filepath.IsAbs(path) {
			t.Fatalf("relative child path%q", path)
		}
	}
}

// assertReexec checks that the daemon child's argv alone reproduces o.
func assertReexec(t *testing.T, o serveOptions) {
	t.Helper()
	clearConfigEnvironment(t)
	child, err := parseCLI(serveArguments(o), &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	o.background = false
	if !reflect.DeepEqual(child.options, o) {
		t.Fatalf("child%+v parent%+v", child.options, o)
	}
}

func joinCLIName(args []string) string {
	if len(args) == 0 {
		return "empty"
	}
	var b bytes.Buffer
	for _, arg := range args {
		b.WriteString(arg)
		b.WriteByte('_')
	}
	return b.String()
}
