package engine

import (
	"fmt"
	"sort"

	"stuntgres/internal/sqlstate"
)

// settingsRegistry owns shared metadata, immutable after construction. Value
// copies intentionally share its map and slice; readers must not mutate them.
type settingsRegistry struct {
	definitions map[string]SettingDefinition
	order       []string
}

func newSettingsRegistry(registered []SettingDefinition, trackCommitTimestamps bool) settingsRegistry {
	r := settingsRegistry{definitions: make(map[string]SettingDefinition, len(builtinSettings)+len(registered))}
	for i, list := range [][]SettingDefinition{builtinSettings, registered} {
		for _, d := range list {
			if i == 0 {
				if d.Name == "track_commit_timestamp" && trackCommitTimestamps {
					d.BootDefault = "on"
				}
				v, err := d.parse(d.BootDefault)
				if err != nil {
					panic(err)
				} // Builtin definitions are source-owned constants.
				d.bootValue = v
				d.BootDefault = d.format(v)
			}
			r.definitions[d.Name] = d
			r.order = append(r.order, d.Name)
		}
	}
	sort.Strings(r.order)
	return r
}

func (r settingsRegistry) Lookup(name string) (SettingDefinition, error) {
	if d, ok := r.definitions[settingFold(name)]; ok {
		return d, nil
	}
	return SettingDefinition{}, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: fmt.Sprintf(`unrecognized configuration parameter "%s"`, name)}
}

// Definitions borrows immutable definitions, including their enum and boot paths.
// Neither the map nor any backing slices may be changed by the caller.
func (r settingsRegistry) Definitions() map[string]SettingDefinition { return r.definitions }

// Order borrows the immutable sorted names; callers must not change the slice.
func (r settingsRegistry) Order() []string { return r.order }
