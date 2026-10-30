package stuntgres

import (
	"strings"

	"stuntgres/internal/engine/core"
)

// TextSearchFile supplies explicit UTF-8 dictionary configuration bytes. Name is
// a lowercase ASCII basename followed by .dict, .affix or .stop, never a path.
// Empty Data supplies an empty file; malformed contents are diagnosed by SQL
// dictionary initialization, not host registration.
type TextSearchFile struct {
	Name string
	Data []byte
}

// RegisterTextSearchFile copies file into this Driver's startup configuration.
// Registrations are independent between Drivers and freeze on the first
// successful connector construction, like settings and foreign definitions.
func (d *Driver) RegisterTextSearchFile(file TextSearchFile) error {
	base, suffix, ok := strings.Cut(file.Name, ".")
	if !ok || base == "" || (suffix != "dict" && suffix != "affix" && suffix != "stop") {
		return core.RegistrationError("invalid text search file name")
	}
	for _, c := range []byte(base) {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '_' {
			return core.RegistrationError("invalid text search file name")
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settingsFrozen {
		return core.RegistrationError("text search files are frozen")
	}
	if _, exists := d.textSearchFiles[file.Name]; exists {
		return core.RegistrationError("duplicate text search file")
	}
	if d.textSearchFiles == nil {
		d.textSearchFiles = make(map[string]string)
	}
	d.textSearchFiles[file.Name] = string(file.Data)
	return nil
}
