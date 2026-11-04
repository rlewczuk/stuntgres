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

// authFiles owns real diagnostic inputs, not loaded authentication state. Cleanup
// is registered before the first mutation and never reloads either server file.
type authFiles struct{ native, postgres, nativeRoot, postgresRoot string }

func newAuthFileScenario(t *testing.T, kind, contents string, manifest map[string]string, configure func(*stuntgres.Driver, string) error) (*Pair, *authFiles) {
	t.Helper()
	root := t.TempDir()
	f := &authFiles{native: filepath.Join(root, "pg_"+kind+".conf"), nativeRoot: root}
	p := NewScenarioWithDriverConfig(t, func(d *stuntgres.Driver) error {
		return configure(d, f.native)
	})
	preparePostgresAuth(t, p, f, kind)
	for _, b := range p.Backends() {
		for name, data := range manifest {
			f.Write(t, p.Ctx, b, name, data)
		}
		f.Write(t, p.Ctx, b, "", contents)
	}
	return p, f
}

func preparePostgresAuth(t *testing.T, p *Pair, f *authFiles, kind string) {
	if p.Postgres != nil {
		if err := p.Postgres.QueryRowContext(p.Ctx, "SHOW "+kind+"_file").Scan(&f.postgres); err != nil {
			t.Fatal(err)
		}
		f.postgresRoot = path.Join(path.Dir(f.postgres), ".stuntgres-"+kind+"-"+filepath.Base(filepath.Dir(f.nativeRoot))+"-"+filepath.Base(f.nativeRoot))
		backup := f.postgresRoot + ".saved"
		saved := false
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			// Independent of the scenario's possibly cancelled context. Do not
			// FailNow here: all restoration/removal operations must be attempted.
			restored := !saved
			if saved {
				_, err := authCommand(ctx, "cp", "-p", "--", backup, f.postgres)
				restored = err == nil
				if err != nil {
					t.Errorf("authentication fixture restore (original retained at %s): %v", backup, err)
				}
			}
			if restored {
				authExec(t, ctx, "rm", "-f", "--", backup)
			}
			authExec(t, ctx, "rm", "-rf", "--", f.postgresRoot)
		})
		authExec(t, p.Ctx, "cp", "-p", "--", f.postgres, backup)
		if t.Failed() {
			t.FailNow()
		}
		saved = true
		authExec(t, p.Ctx, "mkdir", "-p", "--", f.postgresRoot)
	}
}

func authExec(t *testing.T, ctx context.Context, args ...string) []byte {
	t.Helper()
	data, err := authCommand(ctx, args...)
	if err != nil {
		t.Error(err)
	}
	return data
}
func authCommand(ctx context.Context, args ...string) ([]byte, error) {
	code, output, err := Container.Exec(ctx, args, exec.Multiplexed())
	if err != nil {
		return nil, fmt.Errorf("authentication fixture command %q: %w", args, err)
	}
	if closer, ok := output.(io.Closer); ok {
		defer closer.Close()
	}
	data, err := io.ReadAll(output)
	if err != nil || code != 0 {
		return data, fmt.Errorf("authentication fixture %q exit=%d output=%q error=%v", args, code, data, err)
	}
	return data, nil
}
func (f *authFiles) Root(b Backend) string {
	if b.PG {
		return f.postgresRoot
	}
	return f.nativeRoot
}
func (f *authFiles) Path(b Backend, name string) string {
	if name == "" {
		if b.PG {
			return f.postgres
		}
		return f.native
	}
	return path.Join(f.Root(b), name)
}
func (f *authFiles) Write(t *testing.T, ctx context.Context, b Backend, name, data string) {
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
	authExec(t, ctx, "mkdir", "-p", "--", path.Dir(target))
	if err := Container.CopyToContainer(ctx, []byte(data), target, 0o644); err != nil {
		t.Fatal(err)
	}
	if name == "" {
		backup := f.postgresRoot + ".saved"
		authExec(t, ctx, "chown", "--reference="+backup, "--", target)
		authExec(t, ctx, "chmod", "--reference="+backup, "--", target)
	}
}
func (f *authFiles) Remove(t *testing.T, ctx context.Context, b Backend, name string) {
	t.Helper()
	if b.PG {
		authExec(t, ctx, "rm", "-f", "--", f.Path(b, name))
		return
	}
	if err := os.Remove(f.Path(b, name)); err != nil {
		t.Fatal(fmt.Errorf("authentication fixture remove: %w", err))
	}
}
func (f *authFiles) Mkdir(t *testing.T, ctx context.Context, b Backend, name string) {
	t.Helper()
	if b.PG {
		authExec(t, ctx, "mkdir", "-p", "--", f.Path(b, name))
		return
	}
	if err := os.MkdirAll(f.Path(b, name), 0o755); err != nil {
		t.Fatal(err)
	}
}
func (f *authFiles) Symlink(t *testing.T, ctx context.Context, b Backend, name, target string) {
	t.Helper()
	if b.PG {
		authExec(t, ctx, "ln", "-s", "--", target, f.Path(b, name))
		return
	}
	if err := os.Symlink(target, f.Path(b, name)); err != nil {
		t.Fatal(err)
	}
}

// Chmod witnesses require a native process without discretionary-access bypass.
func (f *authFiles) Chmod(t *testing.T, ctx context.Context, b Backend, name string, mode os.FileMode) {
	t.Helper()
	if b.PG {
		original := strings.TrimSpace(string(authExec(t, ctx, "stat", "-c", "%a", "--", f.Path(b, name))))
		t.Cleanup(func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			authExec(t, cleanup, "chmod", original, "--", f.Path(b, name))
		})
		authExec(t, ctx, "chmod", fmt.Sprintf("%o", mode.Perm()), "--", f.Path(b, name))
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
