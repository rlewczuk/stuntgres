package settings

import "sync"

// SessionDefaults are the initial defaults of a session without a
// SessionDefaultProvider: each definition's display text and typed native
// startup default, by name. Every such session of one registry shares them,
// so neither map may be changed.
type SessionDefaults struct {
	Text  map[string]string
	Typed map[string]Value
}

// sessionDefaults memoizes a registry's SessionDefaults; registry copies
// share it.
type sessionDefaults struct {
	once     sync.Once
	defaults SessionDefaults
}

// SessionDefaults returns the registry's provider-less session defaults,
// built once per registry.
func (r Registry) SessionDefaults() SessionDefaults {
	if r.defaults == nil {
		return r.buildSessionDefaults()
	}
	r.defaults.once.Do(func() { r.defaults.defaults = r.buildSessionDefaults() })
	return r.defaults.defaults
}

func (r Registry) buildSessionDefaults() SessionDefaults {
	d := SessionDefaults{Text: make(map[string]string, len(r.order)), Typed: make(map[string]Value, len(r.order))}
	for _, name := range r.order {
		definition := r.definitions[name]
		v := definition.DefaultValue()
		d.Text[definition.Name], d.Typed[definition.Name] = definition.Format(v), v
	}
	return d
}
