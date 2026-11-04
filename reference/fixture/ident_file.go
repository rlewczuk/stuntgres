package fixture

import (
	"testing"

	"stuntgres"
)

// IdentFiles owns an actual native file and a saved/restored oracle ident_file.
// {ROOT} in literal fixture bytes expands to the backend's isolated include root.
// No expected rows or diagnostics are obtained from the view under test.
type IdentFiles struct{ *authFiles }

func NewIdentFileScenario(t *testing.T, contents string, manifest map[string]string) (*Pair, *IdentFiles) {
	t.Helper()
	p, files := newAuthFileScenario(t, "ident", contents, manifest, func(d *stuntgres.Driver, path string) error {
		return d.ConfigureIdentFile(path)
	})
	return p, &IdentFiles{files}
}
