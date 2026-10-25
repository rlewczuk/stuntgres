package main

import (
	"context"
	"encoding/base64"
	"time"

	"stuntgres/internal/engine/session"
)

// queryCapture collects one command cycle for query.log: the frontend
// messages from the first one until ReadyForQuery, and every backend message
// written meanwhile. The serial dispatcher owns it (wireCodec.send runs only on
// the dispatcher), so it needs no lock. A nil capture disables every method.
// Capture starts after admission: startup, authentication and the backend key
// are never recorded.
type queryCapture struct {
	records  uint64 // emitted records on this connection
	open     bool   // a frontend message started the current cycle
	ready    bool   // ReadyForQuery ended the current cycle
	started  time.Time
	frontend []byte
	steps    []queryStep
	current  int // index of the dispatched step that detail fills
	response []queryMessage
	formats  []int16 // latest RowDescription formats, for readable DataRow values
	errCode  string  // SQLSTATE of the cycle's first ErrorResponse
}

// queryStep is one frontend message of the cycle.
type queryStep struct {
	Type    string            `json:"type"`
	Target  string            `json:"target,omitempty"` // Describe/Close: statement or portal
	Name    string            `json:"name,omitempty"`   // statement or portal name
	Query   string            `json:"query,omitempty"`
	Params  []loggedParameter `json:"params,omitempty"`
	Data    string            `json:"data,omitempty"` // exact COPY input body, base64
	Decoded any               `json:"decoded,omitempty"`
}

// queryMessage is one backend message: Data is its exact body (base64) and
// Decoded a best-effort readable form of the same bytes.
type queryMessage struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Data    string `json:"data"`
	Decoded any    `json:"decoded"`
}

// begin records a dispatched frontend message, opening a cycle if needed.
func (q *queryCapture) begin(tag byte) {
	if q == nil {
		return
	}
	q.add(tag)
	q.current = len(q.steps) - 1
}

func (q *queryCapture) add(tag byte) {
	if !q.open {
		q.open, q.started, q.formats = true, time.Now(), nil
	}
	q.frontend = append(q.frontend, tag)
	q.steps = append(q.steps, queryStep{Type: string(tag)})
}

// detail fills the dispatched step's statement or portal name, SQL and
// params. COPY input recorded meanwhile does not move it.
func (q *queryCapture) detail(tag, target byte, name string, context session.ProtocolContext) {
	if q == nil || q.current >= len(q.steps) {
		return
	}
	step := &q.steps[q.current]
	step.Name, step.Query = name, context.Source
	switch target {
	case 'S':
		step.Target = "statement"
	case 'P':
		step.Target = "portal"
	}
	if tag == 'B' || tag == 'E' {
		step.Params = logParameters(context.Parameters)
	}
}

// copyInput records a frontend message consumed by a COPY FROM STDIN stream.
func (q *queryCapture) copyInput(m frontendMessage) {
	if q == nil {
		return
	}
	q.add(m.tag)
	step := &q.steps[len(q.steps)-1]
	if len(m.body) != 0 {
		step.Data = base64.StdEncoding.EncodeToString(m.body)
	}
	switch m.tag {
	case 'd':
		step.Decoded = map[string]any{"data": readableValue(m.body, false)}
	case 'f':
		step.Decoded = map[string]any{"message": readableValue(trimCString(m.body), false)}
	}
}

// backend records one backend message. body may be a reused buffer, so it is
// encoded and decoded now and never retained.
func (q *queryCapture) backend(tag byte, body []byte) {
	if q == nil {
		return
	}
	name, decoded := decodeBackend(tag, body, &q.formats)
	q.response = append(q.response, queryMessage{Type: string(tag), Name: name, Data: base64.StdEncoding.EncodeToString(body), Decoded: decoded})
	if tag == 'E' && q.errCode == "" {
		q.errCode = errorFields(body)["code"]
	}
	if tag == 'Z' && q.open {
		q.ready = true
	}
}

func (q *queryCapture) reset() {
	q.open, q.ready, q.errCode, q.current = false, false, "", 0
	q.frontend, q.steps, q.response, q.formats = q.frontend[:0], nil, nil, nil
}

// emitQuery writes the pending cycle, or output sent outside any cycle (idle
// notifications or a terminal error), as one query.log record. A cycle not
// ended by ReadyForQuery is written with complete=false.
func (c *client) emitQuery() {
	q := c.capture
	if q == nil || (!q.open && len(q.response) == 0) {
		return
	}
	q.records++
	query, params := "", []loggedParameter{}
	paramsSet := false
	for _, step := range q.steps {
		if query == "" {
			query = step.Query
		}
		if !paramsSet && (step.Type == "B" || step.Type == "E") {
			params, paramsSet = step.Params, true
			if params == nil {
				params = []loggedParameter{}
			}
		}
	}
	steps, response := q.steps, q.response
	if steps == nil {
		steps = []queryStep{}
	}
	var duration int64
	if q.open {
		duration = time.Since(q.started).Microseconds()
	}
	err := c.server.logs.query(context.Background(),
		"connection_id", c.id, "database", c.database, "user", c.user,
		"cycle", q.records, "frontend", string(q.frontend), "query", query, "params", params,
		"steps", steps, "response", response, "error", q.errCode,
		"complete", q.ready || !q.open, "duration_us", duration)
	q.reset()
	if err != nil {
		c.server.fail(err)
	}
}
