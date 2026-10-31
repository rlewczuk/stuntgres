package stuntgres

import (
	"path/filepath"
	"strings"

	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/identfile"
)

// ConfigureIdentFile attaches an absolute native identity configuration path.
// Registration does not open the file. The first successful connector freezes
// this capability. Authorized queries read current bytes, including arbitrary
// PostgreSQL includes under process permissions: this is not a directory sandbox.
// This does not change SHOW ident_file and never performs authentication.
func (d *Driver) ConfigureIdentFile(path string) error {
	if path == "" || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return core.RegistrationError("invalid identity file path")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settingsFrozen {
		return core.RegistrationError("identity file is frozen")
	}
	if d.identFile != nil {
		return core.RegistrationError("duplicate identity file")
	}
	d.identFile = identfile.New(filepath.Clean(path))
	return nil
}
