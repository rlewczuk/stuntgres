package reference_test

import (
	"bytes"
	"fmt"
	"github.com/moby/moby/api/pkg/stdcopy"
	"io"
	"os"
	"path/filepath"
	"stuntgres/reference/fixture"
	"testing"
)

func alterSystemOracleFiles(t *testing.T,s *alterSystemState) []byte {
	t.Helper()
	code,out,err := fixture.Container.Exec(s.p.Ctx,[]string{"sh","-c",`set -eu; base64 "$PGDATA/postgresql.auto.conf"; if test -e "$PGDATA/postgresql.auto.conf.tmp"; then printf '\nTMP PRESENT\n'; base64 "$PGDATA/postgresql.auto.conf.tmp"; else printf '\nTMP ABSENT\n'; fi`})
	if err != nil { t.Fatal(err) }
	data,err := io.ReadAll(io.LimitReader(out,1<<20))
	if err != nil || code != 0 || len(data) == 1<<20 { t.Fatalf("auto-file snapshot: exit=%d error=%v output=%q",code,err,data) }
	// Docker multiplex frame boundaries are transport state, not file bytes.
	var stdout, stderr bytes.Buffer
	if _,err := stdcopy.StdCopy(&stdout,&stderr,bytes.NewReader(data)); err != nil {
		t.Fatalf("decode auto-file snapshot: %v",err)
	}
	if stderr.Len() != 0 { t.Fatalf("auto-file snapshot stderr: %s",stderr.Bytes()) }
	return stdout.Bytes()
}

func alterSystemHost(t *testing.T,s *alterSystemState) func() {
	t.Helper()
	if s.b.PG {
		before := alterSystemOracleFiles(t,s)
		return func() {
			t.Helper()
			if after := alterSystemOracleFiles(t,s); !bytes.Equal(before,after) { t.Fatalf("oracle auto files changed: before=%q after=%q",before,after) }
		}
	}
	dir := t.TempDir()
	t.Chdir(dir)
	files := map[string][]byte{"postgresql.auto.conf":[]byte("# untouched auto configuration\napplication_name = 'host-sentinel'\n"),"unrelated":[]byte("untouched\x00bytes")}
	for name,data := range files { if err := os.WriteFile(filepath.Join(dir,name),data,0600); err != nil { t.Fatal(err) } }
	return func() {
		t.Helper()
		for name,want := range files {
			got,err := os.ReadFile(filepath.Join(dir,name))
			if err != nil || !bytes.Equal(want,got) { t.Fatalf("host file %s changed: %q %v",name,got,err) }
		}
		if _,err := os.Stat(filepath.Join(dir,"postgresql.auto.conf.tmp")); !os.IsNotExist(err) { t.Fatalf("auto tmp exists: %v",err) }
	}
}

func TestReferenceAlterSystemHostFiles(t *testing.T) {
	for _, present := range []bool{true,false} {
		t.Run(fmt.Sprintf("existing=%t",present),func(t *testing.T) {
			alterSystemScenario(t,func(t *testing.T,s *alterSystemState) {
				check := alterSystemHost(t,s)
				if !s.b.PG && !present {
					if err := os.Remove("postgresql.auto.conf"); err != nil { t.Fatal(err) }
					check = func() {
						for _,name := range []string{"postgresql.auto.conf","postgresql.auto.conf.tmp"} {
							if _,err := os.Stat(name); !os.IsNotExist(err) { t.Fatalf("created %s: %v",name,err) }
						}
						data,err := os.ReadFile("unrelated")
						if err != nil || string(data) != "untouched\x00bytes" { t.Fatalf("unrelated file: %q %v",data,err) }
					}
				}
				commands := append([]string(nil),alterSystemCommands...)
				if !s.b.PG {
					dir,err := os.Getwd(); if err != nil { t.Fatal(err) }
					commands = append(commands,"ALTER SYSTEM SET data_directory = '"+dir+"'","ALTER SYSTEM SET config_file = '"+dir+"'")
				}
				check()
				for _,q := range commands {
					s.failure(t,s.a,q,loadDiagnostic("0A000",alterSystemMessage),false,false,false)
					check()
					s.unchanged(t)
				}
			})
		})
	}
}
