package host

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
)

// Manifest is logical compatibility only. No closure, pointer, object storage,
// engine OID or callback implementation is serialized.
type Manifest struct {
	Provider, ABI, Name string
	Handler             Name
	Trusted             bool
	Programs            []ProgramManifest
	Conversions         []ConversionManifest
	Defaults            []DefaultDefinition
}
type ProgramManifest struct {
	ID, Version      string
	Inputs           []Type
	Result           Type
}
type ConversionManifest struct {
	ID                  string
	Type                Type
	Bootstrap, From, To bool
	FromName, ToName    Name
}

func (r *Runtime) Manifest() Manifest {
	d := r.definition
	m := Manifest{Provider: d.Provider, ABI: d.ABIVersion, Name: d.Name, Handler: d.HandlerName, Trusted: d.Trusted, Defaults: slices.Clone(d.Defaults)}
	for _, p := range d.Programs {
		m.Programs = append(m.Programs, ProgramManifest{p.ID, p.Version, slices.Clone(p.Inputs), p.Result})
	}
	for _, c := range d.Conversions {
		m.Conversions = append(m.Conversions, ConversionManifest{c.ID, c.Type, c.Bootstrap, c.FromSQL != nil, c.ToSQL != nil, c.FromName, c.ToName})
	}
	slices.SortFunc(m.Programs, func(a, b ProgramManifest) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	slices.SortFunc(m.Conversions, func(a, b ConversionManifest) int {
		if a.ID < b.ID {
			return -1
		}
		if a.ID > b.ID {
			return 1
		}
		return 0
	})
	slices.SortFunc(m.Defaults, func(a, b DefaultDefinition) int {
		if a.Type < b.Type {
			return -1
		}
		if a.Type > b.Type {
			return 1
		}
		return 0
	})
	return m
}
func (r *Runtime) Fingerprint() string {
	data, _ := json.Marshal(r.Manifest())
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
