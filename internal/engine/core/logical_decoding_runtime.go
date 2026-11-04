package core

import (
	"fmt"

	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/logical"
	"stuntgres/internal/sqltype"
)

// LogicalDecoding is pg_logical_slot_{get,peek}[_binary]_changes: acquire the
// slot, snapshot its database's history under Engine.Mu, run the plugin
// without engine locks, then confirm (get only) and release under Engine.Mu.
// Rows are materialized, so an early close still consumes, as in PostgreSQL.
func (h *statementHost) LogicalDecoding(req functions.LogicalDecodingRequest) ([]functions.LogicalDecodingRow, error) {
	if err := h.replicationRole(); err != nil {
		return nil, err
	}
	s := h.scope()
	e := s.Engine()
	if !e.Cluster.logical.wal {
		return nil, logicalWALRequired()
	}
	if err := functions.CheckLogicalDecodingOptions(req.Slot, req.Options); err != nil {
		return nil, err
	}
	name, err := req.Slot.Text()
	if err != nil {
		return nil, err
	}
	decode := logical.Request{}
	if !req.UptoLSN.IsNull() {
		if decode.UptoLSN, err = req.UptoLSN.PGLSN(); err != nil {
			return nil, err
		}
	}
	if !req.UptoChanges.IsNull() {
		n, err := req.UptoChanges.Integer()
		if err != nil {
			return nil, err
		}
		decode.UptoChanges = int32(n)
	}
	options, err := decodingOptions(req.Options)
	if err != nil {
		return nil, err
	}
	e.Mu.Lock()
	slot, err := e.acquireSlotLocked(name, s.Op.Session.Token())
	if err == nil && slot.target != e {
		err = SQLError("55000", fmt.Sprintf(`replication slot "%s" was not created in this database`, name))
		e.releaseSlotLocked(slot)
	}
	e.Mu.Unlock()
	if err != nil {
		return nil, err
	}
	defer func() {
		e.Mu.Lock()
		e.releaseSlotLocked(slot)
		e.Mu.Unlock()
	}()
	plugin, opts, err := h.startPlugin(slot, options, false)
	if err != nil {
		return nil, err
	}
	defer plugin.Shutdown()
	if !req.Binary && opts.Binary {
		return nil, SQLError("0A000", fmt.Sprintf(`logical decoding output plugin "%s" produces binary output, but function "%s" expects textual data`, slot.plugin, req.Function))
	}
	decode.Plugin, decode.PluginName, decode.Options = plugin, slot.plugin, opts
	decode.StreamLimit, decode.StreamImmediate = h.streamingBoundary()
	e.Mu.Lock()
	decode.Records, decode.Restart, decode.Confirmed, decode.TwoPhase, decode.TwoPhaseAt = e.logicalHistory.Snapshot(), slot.restart, slot.confirmed, slot.twoPhase, slot.twoPhaseAt
	decode.Frontier = e.Cluster.logical.lsn.End()
	e.Mu.Unlock()
	result, err := logical.Decode(s.Op.Context(), decode)
	if err != nil {
		return nil, err
	}
	if !req.Peek {
		e.Mu.Lock()
		e.confirmSlotLocked(slot, result.End)
		e.Mu.Unlock()
	}
	rows := make([]functions.LogicalDecodingRow, len(result.Rows))
	for i, row := range result.Rows {
		rows[i] = functions.LogicalDecodingRow{LSN: row.LSN, XID: row.XID, Data: row.Data}
	}
	return rows, nil
}

// decodingOptions pairs the validated one-dimensional, NULL-free options.
func decodingOptions(options sqltype.Value) ([]logical.Option, error) {
	pairs := make([]logical.Option, 0, options.ArrayLen()/2)
	for i := 0; i+1 < options.ArrayLen(); i += 2 {
		name, err := options.ArrayElement(i).Text()
		if err != nil {
			return nil, err
		}
		value, err := options.ArrayElement(i + 1).Text()
		if err != nil {
			return nil, err
		}
		pairs = append(pairs, logical.Option{Name: name, Value: value})
	}
	return pairs, nil
}

// streamingBoundary reads logical_decoding_work_mem (kB) and
// debug_logical_replication_streaming for the plugin's streaming request.
func (h *statementHost) streamingBoundary() (int64, bool) {
	store := h.scope().Op.EffectiveSettings()
	store.Mu.Lock()
	limit := int64(store.TypedValue("logical_decoding_work_mem").Number) << 10
	mode := store.Value("debug_logical_replication_streaming")
	store.Mu.Unlock()
	return limit, mode == "immediate"
}
