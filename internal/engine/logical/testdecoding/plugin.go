// Package testdecoding implements PostgreSQL's stock SQL output plugin.
package testdecoding

import (
	"fmt"
	"strconv"
	"strings"

	"stuntgres/internal/engine/logical"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type state struct {
	begun, wrote, block bool
}

type plugin struct {
	xids, timestamp, skipEmpty, local bool
	options                           logical.Options
	txns                              map[uint32]*state
	output                            sqltype.OutputEnvironment
}

// New creates an independent output context; no decoder state survives Shutdown.
func New() logical.Plugin {
	return &plugin{xids: true}
}

func (p *plugin) Startup(s logical.Startup) (logical.Options, error) {
	p.output = s.Output
	for _, o := range s.Options {
		var target *bool
		switch o.Name {
		case "include-xids":
			target = &p.xids
		case "include-timestamp":
			target = &p.timestamp
		case "skip-empty-xacts":
			target = &p.skipEmpty
		case "only-local":
			target = &p.local
		case "include-rewrites":
			target = &p.options.ReceiveRewrites
		case "stream-changes":
			target = &p.options.Streaming
		case "force-binary":
			value, ok := boolean(o.Value)
			if !ok {
				return logical.Options{}, badBoolean(o)
			}
			p.options.Binary = p.options.Binary || value
			continue
		default:
			return logical.Options{}, diagnostic(fmt.Sprintf("option \"%s\" = \"%s\" is unknown", o.Name, o.Value))
		}
		value, ok := boolean(o.Value)
		if !ok {
			return logical.Options{}, badBoolean(o)
		}
		*target = value
	}
	return p.options, nil
}

func diagnostic(message string) error {
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22023", Message: message}
}

func badBoolean(o logical.Option) error {
	return diagnostic(fmt.Sprintf("could not parse value \"%s\" for parameter \"%s\"", o.Value, o.Name))
}

func boolean(s string) (bool, bool) {
	if s == "1" {
		return true, true
	}
	if s == "0" {
		return false, true
	}
	if s == "" || s == "o" || s == "O" {
		return false, false
	}
	for _, word := range []string{"true", "yes", "on"} {
		if len(s) <= len(word) && strings.EqualFold(word[:len(s)], s) {
			return true, true
		}
	}
	for _, word := range []string{"false", "no", "off"} {
		if len(s) <= len(word) && strings.EqualFold(word[:len(s)], s) {
			return false, true
		}
	}
	return false, false
}

func (p *plugin) state(t *logical.Txn) *state {
	s := p.txns[t.XID]
	if s == nil {
		s = &state{}
		if p.txns == nil {
			p.txns = make(map[uint32]*state)
		}
		p.txns[t.XID] = s
	}
	return s
}

func (p *plugin) xid(t *logical.Txn) string {
	if p.xids {
		return " " + strconv.FormatUint(uint64(t.XID), 10)
	}
	return ""
}

func (p *plugin) stamp(t *logical.Txn) string {
	if !p.timestamp {
		return ""
	}
	return " (at " + sqltype.TransactionTimestampOutput(t.Time, p.output.Zone) + ")"
}

func emit(o *logical.Output, s string) error {
	o.WriteString(s)
	return nil
}

func (p *plugin) Begin(o *logical.Output, t *logical.Txn) error {
	p.state(t)
	if !p.skipEmpty {
		return p.begin(o, t)
	}
	return nil
}

func (p *plugin) begin(o *logical.Output, t *logical.Txn) error {
	s := p.state(t)
	if s.begun {
		return nil
	}
	s.begun = true
	return emit(o, "BEGIN"+p.xid(t))
}

func (p *plugin) Change(o *logical.Output, t *logical.Txn, c *logical.Change) error {
	if err := p.begin(o, t); err != nil {
		return err
	}
	p.state(t).wrote = true
	text, err := formatChange(o.Context(), p.output, c)
	if err != nil {
		return err
	}
	return emit(o, text)
}

func (p *plugin) Truncate(o *logical.Output, t *logical.Txn, c *logical.Change) error {
	return p.Change(o, t, c)
}

func (p *plugin) Commit(o *logical.Output, t *logical.Txn) error {
	s := p.state(t)
	defer delete(p.txns, t.XID)
	if p.skipEmpty && !s.wrote {
		return nil
	}
	return emit(o, "COMMIT"+p.xid(t)+p.stamp(t))
}

func (p *plugin) BeginPrepare(o *logical.Output, t *logical.Txn) error {
	return p.Begin(o, t)
}

func gid(t *logical.Txn) string {
	text := strings.ReplaceAll(t.GID, "'", "''")
	if strings.Contains(text, `\`) {
		return " E'" + strings.ReplaceAll(text, `\`, `\\`) + "'"
	}
	return " '" + text + "'"
}

func (p *plugin) preparedID(t *logical.Txn) string {
	if p.xids {
		return ", txid " + strconv.FormatUint(uint64(t.XID), 10)
	}
	return ""
}

func (p *plugin) Prepare(o *logical.Output, t *logical.Txn) error {
	s := p.state(t)
	defer delete(p.txns, t.XID)
	if p.skipEmpty && !s.wrote {
		return nil
	}
	return emit(o, "PREPARE TRANSACTION"+gid(t)+p.preparedID(t)+p.stamp(t))
}

func (p *plugin) CommitPrepared(o *logical.Output, t *logical.Txn) error {
	return emit(o, "COMMIT PREPARED"+gid(t)+p.preparedID(t)+p.stamp(t))
}

func (p *plugin) RollbackPrepared(o *logical.Output, t *logical.Txn) error {
	return emit(o, "ROLLBACK PREPARED"+gid(t)+p.preparedID(t)+p.stamp(t))
}

func (p *plugin) FilterByOrigin(origin uint32) bool { return p.local && origin != 0 }

func (p *plugin) Shutdown() {
	clear(p.txns)
	p.output = sqltype.OutputEnvironment{}
}

func (p *plugin) streamID(t *logical.Txn) string {
	if p.xids {
		return " TXN " + strconv.FormatUint(uint64(t.XID), 10)
	}
	return ""
}

func (p *plugin) StreamStart(o *logical.Output, t *logical.Txn) error {
	p.state(t).block = false
	if !p.skipEmpty {
		return p.open(o, t)
	}
	return nil
}

func (p *plugin) open(o *logical.Output, t *logical.Txn) error {
	s := p.state(t)
	if s.block {
		return nil
	}
	s.block = true
	return emit(o, "opening a streamed block for transaction"+p.streamID(t))
}

func (p *plugin) StreamChange(o *logical.Output, t *logical.Txn, c *logical.Change) error {
	if err := p.open(o, t); err != nil {
		return err
	}
	p.state(t).wrote = true
	label := "streaming change for "
	if c.Kind == logical.Truncate {
		label = "streaming truncate for "
	}
	if p.xids {
		return emit(o, label+"TXN "+strconv.FormatUint(uint64(t.XID), 10))
	}
	return emit(o, label+"transaction")
}

func (p *plugin) StreamTruncate(o *logical.Output, t *logical.Txn, c *logical.Change) error {
	return p.StreamChange(o, t, c)
}

func (p *plugin) StreamStop(o *logical.Output, t *logical.Txn) error {
	if !p.state(t).block {
		return nil
	}
	return emit(o, "closing a streamed block for transaction"+p.streamID(t))
}

func (p *plugin) StreamAbort(o *logical.Output, t *logical.Txn) error {
	if t.TopXID == 0 || t.TopXID == t.XID {
		defer delete(p.txns, t.XID)
	}
	top := *t
	if t.TopXID != 0 {
		top.XID = t.TopXID
	}
	if p.skipEmpty && !p.state(&top).wrote {
		return nil
	}
	return emit(o, "aborting streamed (sub)transaction"+p.streamID(t))
}

func (p *plugin) StreamCommit(o *logical.Output, t *logical.Txn) error {
	s := p.state(t)
	defer delete(p.txns, t.XID)
	if p.skipEmpty && !s.wrote {
		return nil
	}
	return emit(o, "committing streamed transaction"+p.streamID(t)+p.stamp(t))
}

func (p *plugin) StreamPrepare(o *logical.Output, t *logical.Txn) error {
	s := p.state(t)
	defer delete(p.txns, t.XID)
	if p.skipEmpty && !s.wrote {
		return nil
	}
	label := "preparing streamed transaction"
	if p.xids {
		label += " TXN"
	}
	return emit(o, label+gid(t)+p.preparedID(t)+p.stamp(t))
}
