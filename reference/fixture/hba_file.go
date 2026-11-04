package fixture

import (
	"context"
	"path"
	"strings"
	"testing"

	"stuntgres"
)

// HBARuleSet pairs real PostgreSQL input with independently authored native facts.
// Only explicit {MAIN}/{ROOT} file labels are substituted; Contents is never
// decoded into Rules, and SQL results are never used to construct native facts.
type HBARuleSet struct {
	Contents string
	Manifest map[string]string
	Rules    []stuntgres.HBARule
}

// HBAFiles owns PostgreSQL's temporary diagnostic bytes and a native Driver.
// Native rules are simulated facts; neither backend reloads authentication.
type HBAFiles struct {
	files  *authFiles
	driver *stuntgres.Driver
}

func NewHBAFileScenario(t *testing.T, set HBARuleSet) (*Pair, *HBAFiles) {
	t.Helper()
	p := NewScenario(t)
	// A label only: no native HBA file or directory is created.
	root := path.Join("/stuntgres/simulated/hba", t.Name())
	files := &authFiles{native: path.Join(root, "pg_hba.conf"), nativeRoot: root}
	preparePostgresAuth(t, p, files, "hba")
	f := &HBAFiles{files: files, driver: p.NativeConnector.Driver().(*stuntgres.Driver)}
	for _, b := range p.Backends() {
		f.Install(t, p.Ctx, b, set)
	}
	return p, f
}

func (f *HBAFiles) Path(b Backend, name string) string { return f.files.Path(b, name) }

func (f *HBAFiles) Install(t *testing.T, ctx context.Context, b Backend, set HBARuleSet) {
	t.Helper()
	if b.PG {
		for name, data := range set.Manifest {
			f.files.Write(t, ctx, b, name, data)
		}
		f.files.Write(t, ctx, b, "", set.Contents)
		return
	}
	// Copy only the records whose labels we substitute. SetHBARules owns the
	// complete deep copy, including every other pointer and array.
	rules := append([]stuntgres.HBARule(nil), set.Rules...)
	for i := range rules {
		if rules[i].FileName != nil {
			label := strings.NewReplacer("{MAIN}", f.Path(b, ""), "{ROOT}", f.files.Root(b)).Replace(*rules[i].FileName)
			rules[i].FileName = &label
		}
	}
	f.driver.SetHBARules(rules)
}
