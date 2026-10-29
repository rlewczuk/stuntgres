package stuntgres

import (
	"fmt"
	"stuntgres/internal/engine/host"
)

// Host capabilities execute explicitly supplied compiled Go code, not source
// interpreters or PostgreSQL's C ABI. Register before opening any connector.
type HostName = host.Name
type HostType = host.Type

const (
	HostBool   = host.Bool
	HostInt4   = host.Int4
	HostInt8   = host.Int8
	HostText   = host.Text
	HostHstore = host.Hstore
)

type HostHstoreEntry = host.HstoreEntry
type HostHstoreValue = host.HstoreValue
type HostHstoreMap = host.HstoreMap
type HostSQLValue = host.SQLValue
type HostObject = host.Object
type HostProgramFunc = host.ProgramFunc
type HostHandlerFunc = host.HandlerFunc
type HostFromSQLFunc = host.FromSQLFunc
type HostToSQLFunc = host.ToSQLFunc
type HostConversionDefinition = host.ConversionDefinition
type HostProgramDefinition = host.ProgramDefinition
type HostDefaultDefinition = host.DefaultDefinition
type HostLanguageDefinition = host.LanguageDefinition

// RegisterHostLanguage validates and copies a definition without executing any
// application callback. Stable provider/ABI/program versions are application
// promises of compatible semantics and object representation across Drivers.
func (d *Driver) RegisterHostLanguage(definition HostLanguageDefinition) error {
	runtime, err := host.New(definition)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.settingsFrozen {
		return fmt.Errorf("stuntgres: host registration after first connector")
	}
	for _, r := range d.hostLanguages {
		if r.Name() == runtime.Name() || r.Provider() == runtime.Provider() {
			return fmt.Errorf("stuntgres: duplicate host language registration")
		}
	}
	d.hostLanguages = append(d.hostLanguages, runtime)
	return nil
}
