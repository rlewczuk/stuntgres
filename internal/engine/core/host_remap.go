package core

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/host"
)

// hostReplacementMapper resolves only destination registration capabilities;
// source callbacks never become replacement execution authority.
func (e *Engine) hostReplacementMapper(state *catalog.State) (func(*catalog.Procedure) error, error) {
	runtimes := make(map[*host.Runtime]*host.Runtime)
	runtimeOf := func(source *host.Runtime) (*host.Runtime, error) {
		if source == nil {
			return nil, nil
		}
		if r := runtimes[source]; r != nil {
			return r, nil
		}
		for _, r := range e.Catalog.HostRuntimes {
			if r.Provider() == source.Provider() {
				if r.Fingerprint() != source.Fingerprint() {
					return nil, fmt.Errorf("stuntgres: incompatible destination host runtime")
				}
				runtimes[source] = r
				return r, nil
			}
		}
		return nil, fmt.Errorf("stuntgres: destination host runtime is not registered")
	}
	declarations := make(map[*catalog.HostRoutine]*catalog.HostRoutine)
	conversions := make(map[*catalog.HostConversion]*catalog.HostConversion)
	mapProcedure := func(p *catalog.Procedure) error {
		var err error
		p.HostHandler, err = runtimeOf(p.HostHandler)
		if err != nil {
			return err
		}
		if h := p.Host; h != nil {
			mapped := declarations[h]
			if mapped == nil {
				r, err := runtimeOf(h.Program.Runtime())
				if err != nil {
					return err
				}
				copy := *h
				copy.Program = r.Program(h.Program.ID())
				if copy.Program == nil {
					return fmt.Errorf("stuntgres: destination host program is unavailable")
				}
				copy.Tokens = make([]*host.Conversion, len(h.Tokens))
				for i, token := range h.Tokens {
					copy.Tokens[i] = r.Conversion(token.Definition().ID)
					if copy.Tokens[i] == nil {
						return fmt.Errorf("stuntgres: destination host conversion is unavailable")
					}
				}
				mapped = &copy
				declarations[h] = mapped
				declarations[mapped] = mapped
			}
			p.Host = mapped
		}
		if c := p.HostConversion; c != nil {
			mapped := conversions[c]
			if mapped == nil {
				r, err := runtimeOf(c.Token.Runtime())
				if err != nil {
					return err
				}
				copy := *c
				copy.Token = r.Conversion(c.Token.Definition().ID)
				if copy.Token == nil {
					return fmt.Errorf("stuntgres: destination host conversion is unavailable")
				}
				mapped = &copy
				conversions[c] = mapped
				conversions[mapped] = mapped
			}
			p.HostConversion = mapped
		}
		return nil
	}
	for address, descriptor := range state.Descriptors() {
		switch d := descriptor.(type) {
		case *catalog.Language:
			copy := *d
			var err error
			copy.Host, err = runtimeOf(d.Host)
			if err != nil {
				return nil, err
			}
			copy.HostRegistration, err = runtimeOf(d.HostRegistration)
			if err != nil {
				return nil, err
			}
			state.SetDescriptor(address, &copy)
		case *catalog.Transform:
			copy := *d
			r, err := runtimeOf(d.Runtime)
			if err != nil {
				return nil, err
			}
			copy.Runtime = r
			copy.Conversion = r.Conversion(d.Conversion.Definition().ID)
			state.SetDescriptor(address, &copy)
		}
	}
	return mapProcedure, nil
}
