package main

import (
	"bytes"
	"errors"
	"flag"
	"path/filepath"
	"reflect"
	"testing"
)

func TestServerCLI(t *testing.T) {
	t.Run("aliases_and_reexec", func(t *testing.T) {
		command, err := parseCLI([]string{"start", "-l", "5435", "--listen", "[::1]:0", "-u", "users", "-L", "logs", "-d", "dumps", "-p", "pid", "-r", "-v"}, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		o := command.options
		if o.listen != "[::1]:0" || !o.background || !o.restore || !o.verbose {
			t.Fatal(o)
		}
		for _, path := range []string{o.userfile, o.logdir, o.dumpdir, o.pidfile} {
			if !filepath.IsAbs(path) {
				t.Fatalf("relative child path%q", path)
			}
		}
		child, err := parseCLI(serveArguments(o), &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		o.background = false
		if !reflect.DeepEqual(child.options, o) {
			t.Fatalf("child%+v parent%+v", child.options, o)
		}
	})
	t.Run("password_grants", func(t *testing.T) {
		c, err := parseCLI([]string{"passwd", "-u", "users", "--databases", "CaseSensitive,db--part", "Alice"}, &bytes.Buffer{})
		if err != nil {
			t.Fatal(err)
		}
		if c.username != "Alice" || !c.grantsSet || !reflect.DeepEqual(c.databases, []string{"CaseSensitive", "db--part"}) {
			t.Fatal(c)
		}
		c, err = parseCLI([]string{"passwd", "-u", "users", "Alice"}, &bytes.Buffer{})
		if err != nil || c.grantsSet {
			t.Fatalf("preservation%+v %v", c, err)
		}
	})
	for _, args := range [][]string{
		{}, {"unknown"}, {"serve"}, {"serve", "-u", "users", "-r"}, {"serve", "-u", "users", "extra"},
		{"serve", "-u", "users", "--unsupported"}, {"serve", "-u", "users", "-l", "::1:5434"},
		{"serve", "-u", "users", "-l", "65536"}, {"serve", "-u", "users", "-l", "-1"},
		{"serve", "-u", "users", "-l", "localhost:postgres"}, {"stop", "extra"}, {"dump", "-u", "users"},
		{"passwd", "Alice"}, {"passwd", "-u", "users", "Alice", "Bob"},
		{"passwd", "-u", "users", "--databases", "", "Alice"}, {"passwd", "-u", "users", "--databases", "db,db", "Alice"},
		{"passwd", "-u", "users", "--databases", "../db", "Alice"},
	} {
		t.Run("reject_"+joinCLIName(args), func(t *testing.T) {
			if _, err := parseCLI(args, &bytes.Buffer{}); err == nil {
				t.Fatalf("accepted%v", args)
			}
		})
	}
	for _, args := range [][]string{{"--help"}, {"serve", "--help"}, {"passwd", "--help"}} {
		if _, err := parseCLI(args, &bytes.Buffer{}); !errors.Is(err, flag.ErrHelp) {
			t.Fatalf("help%v: %v", args, err)
		}
	}
	address, err := normalizeListen("0")
	if err != nil || address != "127.0.0.1:0" {
		t.Fatalf("bare port%q %v", address, err)
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
