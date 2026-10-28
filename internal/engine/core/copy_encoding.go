package core

import (
	"stuntgres/internal/engine/copyfmt"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) prepareCopyEncoding(o *copyfmt.Options, from bool) error {
	policy := s.Op.EffectiveSettings().ClientEncoding()
	id := policy.ID()
	if o.EncodingSet {
		id = o.Encoding
	}
	o.Encoding, o.Transport = id, true
	if !from && id == policy.ID() {
		o.Conversion = policy.Outgoing()
		return nil
	}
	source, destination := 6, id
	if from {
		source, destination = id, 6
	}
	var handler sqltype.EncodingConversionHandler
	if source != destination && source != 0 && destination != 0 {
		e := s.Engine()
		e.Mu.Lock()
		err := e.captureVisibilitySnapshotLocked(s)
		e.Mu.Unlock()
		if err != nil {
			return err
		}
		selected, err := s.conversionProcedure(source, destination)
		if err != nil {
			return err
		}
		if selected != nil {
			handler = selected.Conversion
		}
	}
	// COPY FROM resolves its one required direction before G, including binary
	// input. COPY TO for a different file encoding defers a missing direction
	// until text-like startup converts a nonempty NULL marker, header or field
	// after H. Neither is session SET's bidirectional admission diagnostic.
	o.Conversion = sqltype.NewEncodingConversion(source, destination, handler)
	if from && !o.Conversion.Available() {
		return sqltype.MissingBinaryConversion(source, destination)
	}
	return nil
}

type copyTransportRows struct {
	copyfmt.RowSource
	settings *session.SettingsStore
}

func (r copyTransportRows) OutputEnvironment() sqltype.OutputEnvironment {
	env := r.RowSource.OutputEnvironment()
	env.ByteTransport = true
	env.Transport = r.settings.ClientEncoding()
	return env
}
