package fdw

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/sqlstate"
)

// fakeTx records foreign transaction callbacks in a shared log. fail names
// the one call that returns an error. Rollback and EndStatement must ignore
// cancellation, so they log a trailing "!" when their context is canceled.
type fakeTx struct {
	log  *[]string
	name string
	fail string
}

func (x *fakeTx) call(c string) error {
	*x.log = append(*x.log, x.name+c)
	if c == x.fail {
		return errors.New(x.name + c + " failed")
	}
	return nil
}
func canceled(ctx context.Context) string {
	if ctx.Err() != nil {
		return "!"
	}
	return ""
}
func (x *fakeTx) BeginStatement(context.Context) error { return x.call("begin-statement") }
func (x *fakeTx) EndStatement(ctx context.Context, success bool) error {
	if success {
		return x.call("end-success" + canceled(ctx))
	}
	return x.call("end-failure" + canceled(ctx))
}
func (x *fakeTx) Commit(context.Context) error       { return x.call("commit") }
func (x *fakeTx) Rollback(ctx context.Context) error { return x.call("rollback" + canceled(ctx)) }

type fakeSavepointTx struct{ fakeTx }

func (x *fakeSavepointTx) Savepoint(_ context.Context, name string) error {
	return x.call("save:" + name)
}
func (x *fakeSavepointTx) RollbackTo(_ context.Context, name string) error {
	return x.call("rollback:" + name)
}
func (x *fakeSavepointTx) Release(_ context.Context, name string) error {
	return x.call("release:" + name)
}

type fakeProvider struct {
	log          *[]string
	capabilities catalog.ForeignTransactionCapabilities
	capErr       error
	tx           catalog.ForeignTransaction
	beginErr     error
}

func (p *fakeProvider) ForeignTransactionCapabilities(context.Context, catalog.ForeignRequest) (catalog.ForeignTransactionCapabilities, error) {
	*p.log = append(*p.log, "capabilities")
	return p.capabilities, p.capErr
}
func (p *fakeProvider) BeginForeignTransaction(context.Context, catalog.ForeignRequest) (catalog.ForeignTransaction, error) {
	*p.log = append(*p.log, "begin")
	return p.tx, p.beginErr
}

// TestFDWBeginParticipant pins the provider handshake for a new participant:
// capability gates before Begin, savepoint replay in mark order, and one
// uncancelable rollback for every failure after Begin returned a transaction.
func TestFDWBeginParticipant(t *testing.T) {
	full := catalog.ForeignTransactionCapabilities{StatementRollback: true, TransactionRollback: true, Savepoints: true}
	noSavepoints := catalog.ForeignTransactionCapabilities{StatementRollback: true, TransactionRollback: true}
	marks := []session.SavepointMark{{Serial: 3}, {Serial: 5}}
	capFailure := errors.New("capabilities failed")
	beginFailure := errors.New("begin failed")
	plain := func(log *[]string, fail string) catalog.ForeignTransaction { return &fakeTx{log: log, fail: fail} }
	savepoints := func(log *[]string, fail string) catalog.ForeignTransaction {
		return &fakeSavepointTx{fakeTx{log: log, fail: fail}}
	}
	cases := []struct {
		name     string
		caps     catalog.ForeignTransactionCapabilities
		capErr   error
		tx       func(*[]string, string) catalog.ForeignTransaction
		fail     string
		beginErr error
		marks    []session.SavepointMark
		err      error  // wrapped callback error, when set
		code     string // SQLSTATE, when set
		message  string // error text, when set
		log      []string
	}{
		{name: "Replay", caps: full, tx: savepoints, marks: marks,
			log: []string{"capabilities", "begin", "save:stuntgres_sql_3", "save:stuntgres_sql_5"}},
		{name: "NoMarks", caps: noSavepoints, tx: plain, log: []string{"capabilities", "begin"}},
		{name: "CapabilitiesError", caps: full, capErr: capFailure, err: capFailure, log: []string{"capabilities"}},
		{name: "NoStatementRollback", caps: catalog.ForeignTransactionCapabilities{TransactionRollback: true, Savepoints: true},
			code: "0A000", message: "foreign writes require statement and transaction rollback support", log: []string{"capabilities"}},
		{name: "NoTransactionRollback", caps: catalog.ForeignTransactionCapabilities{StatementRollback: true, Savepoints: true},
			code: "0A000", message: "foreign writes require statement and transaction rollback support", log: []string{"capabilities"}},
		{name: "SavepointsUnsupported", caps: noSavepoints, tx: plain, marks: marks,
			code: "0A000", message: "foreign transaction does not support savepoints", log: []string{"capabilities"}},
		{name: "BeginErrorWithTransaction", caps: noSavepoints, tx: plain, beginErr: beginFailure, err: beginFailure,
			log: []string{"capabilities", "begin", "rollback"}},
		{name: "BeginErrorWithoutTransaction", caps: noSavepoints, beginErr: beginFailure, err: beginFailure,
			log: []string{"capabilities", "begin"}},
		{name: "NoTransaction", caps: noSavepoints, message: "stuntgres: foreign provider returned no transaction",
			log: []string{"capabilities", "begin"}},
		{name: "SavepointsNotImplemented", caps: full, tx: plain,
			message: "stuntgres: foreign savepoint capability has no implementation", log: []string{"capabilities", "begin", "rollback"}},
		{name: "ReplayFailure", caps: full, tx: savepoints, fail: "save:stuntgres_sql_5", marks: marks,
			message: "save:stuntgres_sql_5 failed",
			log:     []string{"capabilities", "begin", "save:stuntgres_sql_3", "save:stuntgres_sql_5", "rollback"}},
	}
	key := ParticipantKey{Handler: 1, Server: 2, Mapping: 3, Role: 4}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var log []string
			provider := &fakeProvider{log: &log, capabilities: c.caps, capErr: c.capErr, beginErr: c.beginErr}
			if c.tx != nil {
				provider.tx = c.tx(&log, c.fail)
			}
			p, err := BeginParticipant(ctx, provider, key, catalog.ForeignRequest{ServerOID: 2}, c.marks)
			if !reflect.DeepEqual(log, c.log) {
				t.Fatalf("calls = %q, want %q", log, c.log)
			}
			if c.err == nil && c.code == "" && c.message == "" {
				if err != nil {
					t.Fatal(err)
				}
				want := &Participant{Key: key, Tx: provider.tx, Capabilities: c.caps}
				if !reflect.DeepEqual(p, want) {
					t.Fatalf("participant = %+v, want %+v", p, want)
				}
				return
			}
			if p != nil || err == nil {
				t.Fatalf("participant %+v, error %v", p, err)
			}
			if c.err != nil && !errors.Is(err, c.err) {
				t.Fatalf("error = %v, want %v", err, c.err)
			}
			if c.code != "" {
				if d, ok := err.(*sqlstate.Error); !ok || d.Code != c.code || d.Message != c.message {
					t.Fatalf("error = %#v, want %s %q", err, c.code, c.message)
				}
			} else if c.message != "" && err.Error() != c.message {
				t.Fatalf("error = %q, want %q", err, c.message)
			}
		})
	}
}

// TestFDWEndStatements pins the statement end loop: closed participants are
// skipped, a failed release closes and rolls back that participant once and
// turns success off for the rest, and an earlier primary error wins.
func TestFDWEndStatements(t *testing.T) {
	var log []string
	participants := []*Participant{
		{Tx: &fakeTx{log: &log, name: "a:"}, Closed: true},
		{Tx: &fakeTx{log: &log, name: "b:", fail: "end-success"}},
		{Tx: &fakeTx{log: &log, name: "c:"}},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := EndStatements(ctx, participants, nil)
	if err == nil || err.Error() != "b:end-success failed" {
		t.Fatalf("error = %v", err)
	}
	if want := []string{"b:end-success", "b:rollback", "c:end-failure"}; !reflect.DeepEqual(log, want) {
		t.Fatalf("calls = %q, want %q", log, want)
	}
	if !participants[1].Closed || participants[2].Closed {
		t.Fatal("only the failed participant closes")
	}
	log = nil
	primary := errors.New("statement failed")
	if err := EndStatements(ctx, participants, primary); err != primary {
		t.Fatalf("error = %v, want the primary error", err)
	}
	if want := []string{"c:end-failure"}; !reflect.DeepEqual(log, want) {
		t.Fatalf("calls = %q, want %q", log, want)
	}
	if err := EndStatements(ctx, nil, nil); err != nil {
		t.Fatal(err)
	}
}

// TestFDWGroupTruncate pins TRUNCATE's server grouping: one group per server
// in first-seen order, copied table requests in selection order, and the
// statement's CASCADE and RESTART IDENTITY on every group.
func TestFDWGroupTruncate(t *testing.T) {
	if groups := GroupTruncate(nil, true, true); groups != nil {
		t.Fatalf("groups = %+v, want none", groups)
	}
	source := func(server, table uint32) *exec.ForeignQuerySource {
		return &exec.ForeignQuerySource{Request: catalog.ForeignRequest{
			ServerOID: server, TableOID: table, TableOptions: []catalog.ForeignOption{{Name: "t", Value: "1"}},
		}}
	}
	a, b, c := source(1, 10), source(2, 20), source(1, 30)
	groups := GroupTruncate([]*exec.ForeignQuerySource{a, b, c}, true, false)
	if len(groups) != 2 || groups[0].Source != a || groups[1].Source != b {
		t.Fatalf("groups = %+v", groups)
	}
	tables := func(g TruncateGroup) []uint32 {
		var oids []uint32
		for _, r := range g.Request.Tables {
			oids = append(oids, r.TableOID)
		}
		return oids
	}
	if !reflect.DeepEqual(tables(groups[0]), []uint32{10, 30}) || !reflect.DeepEqual(tables(groups[1]), []uint32{20}) {
		t.Fatal(tables(groups[0]), tables(groups[1]))
	}
	for _, g := range groups {
		if !g.Request.Cascade || g.Request.RestartIdentity || g.Request.Transaction != nil {
			t.Fatalf("request = %+v", g.Request)
		}
	}
	groups[0].Request.Tables[0].TableOptions[0].Value = "2"
	if a.Request.TableOptions[0].Value != "1" {
		t.Fatal("table request shares the source's options")
	}
	var log []string
	tx := &fakeTx{log: &log}
	groups[0].SetTransaction(tx)
	if groups[0].Request.Transaction != tx || groups[0].Request.Tables[0].Transaction != tx || groups[0].Request.Tables[1].Transaction != tx {
		t.Fatal("transaction not set on the group and its tables")
	}
	if groups[1].Request.Transaction != nil || groups[1].Request.Tables[0].Transaction != nil {
		t.Fatal("transaction set on another group")
	}
	if len(log) != 0 {
		t.Fatal(log)
	}
}
