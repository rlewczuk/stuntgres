package fixture

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/exec"
	"stuntgres"
)

// IdentFiles owns an actual native file and a saved/restored oracle ident_file.
// {ROOT} in literal fixture bytes expands to the backend's isolated include root.
// No expected rows or diagnostics are obtained from the view under test.
type IdentFiles struct{ native, postgres, nativeRoot, postgresRoot string }

func NewIdentFileScenario(t *testing.T, contents string, manifest map[string]string) (*Pair, *IdentFiles) {
	t.Helper()
	root := t.TempDir()
	files := &IdentFiles{native: filepath.Join(root, "pg_ident.conf"), nativeRoot: root}
	p := NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error { return d.ConfigureIdentFile(files.native) })
	// Cleanup must precede Pair cleanup, which closes the scenario pools.
	if p.Postgres != nil {
		if err := p.Postgres.QueryRowContext(p.Ctx, "SHOW ident_file").Scan(&files.postgres); err != nil {
			t.Fatal(err)
		}
		files.postgresRoot = path.Join(path.Dir(files.postgres), ".stuntgres-ident-"+filepath.Base(filepath.Dir(root))+"-"+filepath.Base(root))
		backup := files.postgresRoot + ".saved"
		saved := false
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			// cp -p restores the genuine original bytes, owner, group and permission bits.
			if saved {
				identExec(t, ctx, "cp", "-p", "--", backup, files.postgres)
			}
			identExec(t, ctx, "rm", "-f", "--", backup)
			identExec(t, ctx, "rm", "-rf", "--", files.postgresRoot)
		})
		identExec(t, p.Ctx, "cp", "-p", "--", files.postgres, backup)
		saved = true
		identExec(t, p.Ctx, "mkdir", "-p", "--", files.postgresRoot)
	}
	for _, b := range p.Backends() {
		for name, data := range manifest {
			files.Write(t, p.Ctx, b, name, data)
		}
		files.Write(t, p.Ctx, b, "", contents)
	}
	return p, files
}

func identExec(t *testing.T, ctx context.Context, args ...string) []byte {
	t.Helper()
	code, output, err := Container.Exec(ctx, args, exec.Multiplexed())
	if err != nil {
		t.Fatal("identity fixture command", args, err)
	}
	if closer, ok := output.(io.Closer); ok {
		defer closer.Close()
	}
	data, err := io.ReadAll(output)
	if err != nil || code != 0 {
		t.Fatalf("identity fixture %q exit=%d output=%q error=%v", args, code, data, err)
	}
	return data
}

func (f *IdentFiles) Root(b Backend) string {
	if b.PG {
		return f.postgresRoot
	}
	return f.nativeRoot
}
func (f *IdentFiles) Path(b Backend, name string) string {
	if name == "" {
		if b.PG {
			return f.postgres
		}
		return f.native
	}
	return path.Join(f.Root(b), name)
}
func (f *IdentFiles) Write(t *testing.T, ctx context.Context, b Backend, name, data string) {
	t.Helper()
	target := f.Path(b, name)
	data = strings.ReplaceAll(data, "{ROOT}", f.Root(b))
	if !b.PG {
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	identExec(t, ctx, "mkdir", "-p", "--", path.Dir(target))
	if err := Container.CopyToContainer(ctx, []byte(data), target, 0o644); err != nil {
		t.Fatal(err)
	}
	if name == "" {
		backup := f.postgresRoot + ".saved"
		identExec(t, ctx, "chown", "--reference="+backup, "--", target)
		identExec(t, ctx, "chmod", "--reference="+backup, "--", target)
	}
}
func (f *IdentFiles) Remove(t *testing.T, ctx context.Context, b Backend, name string) {
	t.Helper()
	if b.PG {
		identExec(t, ctx, "rm", "-f", "--", f.Path(b, name))
		return
	}
	if err := os.Remove(f.Path(b, name)); err != nil {
		t.Fatal(fmt.Errorf("identity fixture remove: %w", err))
	}
}
func (f *IdentFiles) Mkdir(t *testing.T, ctx context.Context, b Backend, name string) {
	t.Helper()
	if b.PG {
		identExec(t, ctx, "mkdir", "-p", "--", f.Path(b, name))
		return
	}
	if err := os.MkdirAll(f.Path(b, name), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (f *IdentFiles) Symlink(t *testing.T, ctx context.Context, b Backend, name, target string) {
	t.Helper()
	if b.PG {
		identExec(t, ctx, "ln", "-s", "--", target, f.Path(b, name))
		return
	}
	if err := os.Symlink(target, f.Path(b, name)); err != nil {
		t.Fatal(err)
	}
}

// Chmod changes a scenario-owned actual file's permissions. Permission-denial
// witnesses require a native test process without OS discretionary-access bypass.
func (f *IdentFiles) Chmod(t *testing.T, ctx context.Context, b Backend, name string, mode os.FileMode) {
	t.Helper()
	if b.PG {
		original := strings.TrimSpace(string(identExec(t, ctx, "stat", "-c", "%a", "--", f.Path(b, name))))
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			identExec(t, cleanup, "chmod", original, "--", f.Path(b, name))
		})
		identExec(t, ctx, "chmod", fmt.Sprintf("%o", mode.Perm()), "--", f.Path(b, name))
		return
	}
	info, err := os.Stat(f.Path(b, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(f.Path(b, name), info.Mode().Perm()); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chmod(f.Path(b, name), mode); err != nil {
		t.Fatal(err)
	}
}
