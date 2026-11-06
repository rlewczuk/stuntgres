package dual

import (
	"database/sql/driver"
	"fmt"
	"time"
)

// entry is one call's record under construction. Its summary and compare
// callbacks run inside finish, under panic recovery, in the caller's goroutine.
type entry struct {
	c          *conn
	rec        record
	start, end time.Time // call start; native end (rows: Close)
	nerr       error     // native error
	ran        bool      // the reference call ran
	rerr       error     // reference error
	failure    string    // the reference failure transition of this call
	refDur     time.Duration
	uncompared string // why both ran but are not compared
	hideRefErr bool   // the reference error lies beyond what native reached
	summary    func() // fills rec.Response
	compare    func(*diffLines) bool
}

// finish classifies, compares and hands the record to the emitter. A panic in
// a hook or comparison becomes a diff line; it never reaches the caller.
func (e *entry) finish() {
	var d diffLines
	func() {
		defer func() {
			if r := recover(); r != nil {
				d.add(fmt.Sprintf("dual: comparison failed: %v", r))
			}
		}()
		e.fill(&e.c.cn.opts, &d)
	}()
	if e.rec.Response == nil {
		e.rec.Response = empty{}
	}
	if e.rec.ResponseRef == nil {
		e.rec.ResponseRef = empty{}
	}
	e.rec.ResponseDiff = d.finish()
	e.rec.DurationUS = e.end.Sub(e.start).Microseconds()
	e.rec.DurationRefUS = e.refDur.Microseconds()
	e.c.out.emit(e.rec.Cycle, e.c.cn.log.encode(&e.rec))
}

func (e *entry) fill(o *Options, d *diffLines) {
	if e.summary != nil {
		e.summary()
	}
	n := classify(e.nerr, o)
	e.rec.Error, e.rec.Errmsg = n.code, n.text()
	switch {
	case e.rec.Unmirrored != "":
		e.rec.ErrorRef, e.rec.ErrmsgRef = e.c.ref.code, e.c.ref.message
		return
	case e.failure != "":
		r := classify(e.rerr, o)
		e.rec.ErrorRef, e.rec.ErrmsgRef = r.code, e.failure
		if r.class != classSQL {
			e.rec.ErrorRef = e.c.ref.code
		}
		d.add(e.failure)
		return
	}
	r := classify(e.rerr, o)
	e.rec.ErrorRef, e.rec.ErrmsgRef = r.code, r.text()
	if e.uncompared == "" && o.uncompared(e.rec.Call) {
		e.uncompared = "option"
	}
	if e.rec.Uncompared = e.uncompared; e.uncompared != "" {
		return
	}
	if !e.hideRefErr {
		compareErrors(d, n, r, o.CompareErrorDetails)
	}
	if e.compare != nil {
		e.rec.OrderOnly = e.compare(d)
	}
}

// execResult sets the summaries and comparison of an Exec or Stmt Exec call.
func (e *entry) execResult(res driver.Result, err error, out refOutcome[any]) {
	e.summary = func() { e.rec.Response = summarizeResult(res, err) }
	if e.ran && out.failure == "" {
		e.rec.ResponseRef = out.value
	}
	e.compare = func(d *diffLines) bool {
		n, ok := e.rec.Response.(execSummary)
		r, refOK := e.rec.ResponseRef.(execSummary)
		if ok && refOK {
			compareExec(d, n, r)
		}
		return false
	}
}

// Response summaries, in documented JSON field order.
type (
	empty       struct{}
	execSummary struct {
		RowsAffected *int64 `json:"rows_affected"`
		LastInsertID *int64 `json:"last_insert_id"`
	}
	prepSummary struct {
		NumInput int `json:"num_input"`
	}
	validSummary struct {
		Valid bool `json:"valid"`
	}
	setSummary struct {
		Columns []string `json:"columns"`
		Types   []string `json:"types"`
		Rows    int      `json:"rows"`
		Partial bool     `json:"partial"`
	}
	rowsSummary struct {
		Sets     []setSummary `json:"sets"`
		Complete bool         `json:"complete"`
	}
)

// summarizeResult reads a Result's two values; null marks a method error.
func summarizeResult(r driver.Result, err error) any {
	if err != nil || r == nil {
		return empty{}
	}
	var s execSummary
	if n, err := r.RowsAffected(); err == nil {
		s.RowsAffected = &n
	}
	if n, err := r.LastInsertId(); err == nil {
		s.LastInsertID = &n
	}
	return s
}

func summarizeRows(o *rowsOutcome) rowsSummary {
	s := rowsSummary{Sets: make([]setSummary, 0, len(o.sets)), Complete: o.complete}
	for _, set := range o.sets {
		s.Sets = append(s.Sets, setSummary{Columns: nonNil(set.columns), Types: nonNil(set.types), Rows: len(set.rows), Partial: set.partial})
	}
	return s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
