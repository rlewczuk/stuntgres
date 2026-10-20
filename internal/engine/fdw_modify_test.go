package engine

import (
	"context"
	"database/sql/driver"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

type fdwUnitScanWrapper struct{}

func (fdwUnitScanWrapper) GetForeignRelSize(context.Context, catalog.ForeignRequest) (catalog.ForeignRelSize, error) {
	return catalog.ForeignRelSize{}, nil
}
func (fdwUnitScanWrapper) GetForeignPaths(context.Context, catalog.ForeignRequest, catalog.ForeignRelSize) ([]catalog.ForeignPath, error) {
	return nil, nil
}
func (fdwUnitScanWrapper) GetForeignPlan(context.Context, catalog.ForeignRequest, catalog.ForeignPath) (catalog.ForeignPlan, error) {
	return catalog.ForeignPlan{}, nil
}
func (fdwUnitScanWrapper) BeginForeignScan(context.Context, catalog.ForeignRequest, catalog.ForeignPlan) (catalog.ForeignScan, error) {
	return nil, nil
}

type fdwUnitInsert struct {
	present bool
	calls   int
	err     error
}

func (p *fdwUnitInsert) ExecForeignInsert(context.Context, catalog.ForeignModifyRequest, any, catalog.ForeignRow) (catalog.ForeignRow, bool, error) {
	p.calls++
	return catalog.ForeignRow{}, p.present, p.err
}

type fdwUnitBatch struct {
	size      int
	sizes     []int
	oversized bool
}

func (p *fdwUnitBatch) GetForeignModifyBatchSize(context.Context, catalog.ForeignModifyRequest, any) (int, error) {
	return p.size, nil
}
func (p *fdwUnitBatch) ExecForeignBatchInsert(_ context.Context, _ catalog.ForeignModifyRequest, _ any, rows []catalog.ForeignRow) ([]catalog.ForeignRow, error) {
	p.sizes = append(p.sizes, len(rows))
	if p.oversized {
		return append(rows, catalog.ForeignRow{}), nil
	}
	return rows, nil
}

type fdwUnitSingle struct {
	fdwUnitScanWrapper
	*fdwUnitInsert
}
type fdwUnitBatchWriter struct {
	fdwUnitSingle
	*fdwUnitBatch
}
type fdwUnitOnlyBatch struct {
	fdwUnitSingle
	catalog.ForeignBatchInserter
}
type fdwUnitOnlySize struct {
	fdwUnitSingle
	catalog.ForeignBatchSizer
}
type fdwUnitBatchOnly struct {
	fdwUnitScanWrapper
	catalog.ForeignBatchInserter
}

func TestFDWModifyCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wrapper catalog.ForeignDataWrapper
		count   int64
		calls   int
		bad     bool
	}{
		{"single", fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true}}, 5, 5, false},
		{"suppressed", fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{}}, 0, 5, false},
		{"error_wins", fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true, err: errors.New("deliberate insert failure")}}, 0, 1, true},
		{"batch", fdwUnitBatchWriter{fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true}}, fdwUnitBatch: &fdwUnitBatch{size: 2}}, 5, 0, false},
		{"size_one", fdwUnitBatchWriter{fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true}}, fdwUnitBatch: &fdwUnitBatch{size: 1}}, 5, 5, false},
		{"zero_size", fdwUnitBatchWriter{fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true}}, fdwUnitBatch: &fdwUnitBatch{}}, 0, 0, true},
		{"negative_size", fdwUnitBatchWriter{fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true}}, fdwUnitBatch: &fdwUnitBatch{size: -1}}, 0, 0, true},
		{"oversized", fdwUnitBatchWriter{fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true}}, fdwUnitBatch: &fdwUnitBatch{size: 2, oversized: true}}, 0, 0, true},
		{"missing_size", fdwUnitOnlyBatch{fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true}}, ForeignBatchInserter: &fdwUnitBatch{size: 0}}, 5, 5, false},
		{"missing_batch", fdwUnitOnlySize{fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true}}, ForeignBatchSizer: &fdwUnitBatch{size: 0}}, 5, 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session := sessionStart(t, New(), nil)
			op := transactionOperation(t, session)
			defer op.Done()
			s := newStatementScope(op, session.transactions, statementOperationStart(op))
			m := &foreignModification{source: &foreignQuerySource{Wrapper: tc.wrapper}}
			count, err := m.insertRows(context.Background(), s, make([][]sqltype.Value, 5), false, nil)
			if (err != nil) != tc.bad || count != tc.count {
				t.Fatalf("count=%d err=%v", count, err)
			}
			var singles *fdwUnitInsert
			switch p := tc.wrapper.(type) {
			case fdwUnitSingle:
				singles = p.fdwUnitInsert
			case fdwUnitBatchWriter:
				singles = p.fdwUnitInsert
				if tc.name == "batch" && !reflect.DeepEqual(p.sizes, []int{2, 2, 1}) {
					t.Fatal(p.sizes)
				}
			case fdwUnitOnlyBatch:
				singles = p.fdwUnitInsert
			case fdwUnitOnlySize:
				singles = p.fdwUnitInsert
			}
			if singles.calls != tc.calls {
				t.Fatal("single calls", singles.calls)
			}
		})
	}
	if foreignOperationSupported(fdwUnitScanWrapper{}, catalog.ForeignInsert, "f") == nil {
		t.Fatal("scan-only INSERT accepted")
	}
	if foreignOperationSupported(fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{}}, catalog.ForeignUpdate, "f") == nil {
		t.Fatal("INSERT implied UPDATE")
	}
	if foreignOperationSupported(fdwUnitBatchOnly{ForeignBatchInserter: &fdwUnitBatch{size: 2}}, catalog.ForeignInsert, "f") == nil {
		t.Fatal("batch-only provider implied INSERT")
	}
}
func TestFDWModifyIdentity(t *testing.T) {
	t.Run("KeyedMembership", fdwIdentityMembership)
	bytes := []byte("key")
	identity := catalog.ForeignRowIdentity{int64(7), bytes, nil}
	owned := exec.CopyForeignCells(identity)
	bytes[0] = 'X'
	if !foreignIdentityEqual(owned, catalog.ForeignRowIdentity{int64(7), []byte("key"), nil}) || foreignIdentityEqual(identity, owned) {
		t.Fatal("identity ownership/equality")
	}
	if foreignIdentityEqual(owned, catalog.ForeignRowIdentity{int64(7)}) || foreignIdentityEqual(owned, catalog.ForeignRowIdentity{int64(8), []byte("key"), nil}) {
		t.Fatal("identity aliases a different target")
	}
	clock := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	for _, tc := range []struct {
		name  string
		a, b  catalog.ForeignRowIdentity
		equal bool
	}{
		{"empty", nil, catalog.ForeignRowIdentity{}, true},
		{"nil-cell", catalog.ForeignRowIdentity{nil}, catalog.ForeignRowIdentity{nil}, true},
		{"nil-byte-vs-nil", catalog.ForeignRowIdentity{[]byte(nil)}, catalog.ForeignRowIdentity{nil}, false},
		{"nil-vs-empty-bytes", catalog.ForeignRowIdentity{[]byte(nil)}, catalog.ForeignRowIdentity{[]byte{}}, true},
		{"owned-bytes", catalog.ForeignRowIdentity{[]byte("key")}, catalog.ForeignRowIdentity{[]byte("key")}, true},
		{"bytes-vs-text", catalog.ForeignRowIdentity{[]byte("key")}, catalog.ForeignRowIdentity{"key"}, false},
		{"cell-boundaries", catalog.ForeignRowIdentity{[]byte("ab"), []byte("c")}, catalog.ForeignRowIdentity{[]byte("a"), []byte("bc")}, false},
		{"numeric-types", catalog.ForeignRowIdentity{int64(1)}, catalog.ForeignRowIdentity{float64(1)}, false},
		{"signed-zero", catalog.ForeignRowIdentity{float64(0)}, catalog.ForeignRowIdentity{math.Copysign(0, -1)}, true},
		{"nan", catalog.ForeignRowIdentity{math.NaN()}, catalog.ForeignRowIdentity{math.NaN()}, false},
		{"boolean", catalog.ForeignRowIdentity{true}, catalog.ForeignRowIdentity{true}, true},
		{"strict-time-equal", catalog.ForeignRowIdentity{clock}, catalog.ForeignRowIdentity{clock}, true},
		{"strict-time-location", catalog.ForeignRowIdentity{clock}, catalog.ForeignRowIdentity{clock.In(time.FixedZone("other", 0))}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if foreignIdentityEqual(tc.a, tc.b) != tc.equal || foreignIdentityEqual(tc.b, tc.a) != tc.equal {
				t.Fatal("typed opaque identity equality changed", tc.a, tc.b, tc.equal)
			}
		})
	}
}

type fdwUnitLifecycle struct {
	fdwUnitSingle
	tx                   *fdwUnitTransaction
	beginError, endError error
	begins, ends         int
	check                func()
}

func (p *fdwUnitLifecycle) ForeignTransactionCapabilities(context.Context, catalog.ForeignRequest) (catalog.ForeignTransactionCapabilities, error) {
	if p.check != nil {
		p.check()
	}
	return catalog.ForeignTransactionCapabilities{StatementRollback: true, TransactionRollback: true, Savepoints: true}, nil
}
func (p *fdwUnitLifecycle) BeginForeignTransaction(context.Context, catalog.ForeignRequest) (catalog.ForeignTransaction, error) {
	return p.tx, nil
}
func (p *fdwUnitLifecycle) BeginForeignModify(context.Context, catalog.ForeignModifyRequest) (any, error) {
	p.begins++
	if p.check != nil {
		p.check()
	}
	return "partial", p.beginError
}
func (p *fdwUnitLifecycle) EndForeignModify(ctx context.Context, _ catalog.ForeignModifyRequest, state any) error {
	p.ends++
	if p.check != nil {
		p.check()
	}
	if ctx.Err() != nil || state != "partial" {
		return errors.New("invalid cleanup context/state")
	}
	return p.endError
}
func TestFDWModifyLifecycle(t *testing.T) {
	t.Run("CopyInputVeto", fdwUnitCopyVeto)
	for _, partial := range []bool{false, true} {
		e := New()
		session := sessionStart(t, e, nil)
		op := transactionOperation(t, session)
		ref, err := e.Begin(op, TxOptions{})
		if err != nil {
			t.Fatal(err)
		}
		s, err := e.StartStatement(op)
		if err != nil {
			t.Fatal(err)
		}
		s.txRef = ref
		failure := errors.New("callback failure")
		defer func() { _ = s.Finish(failure); op.Done() }()
		p := &fdwUnitLifecycle{fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true}}, tx: &fdwUnitTransaction{}}
		p.check = func() {
			if !e.mu.TryLock() {
				t.Error("engine locked")
			} else {
				e.mu.Unlock()
			}
			if !s.owner.mu.TryLock() {
				t.Error("owner locked")
			} else {
				s.owner.mu.Unlock()
			}
			if !session.mu.TryLock() {
				t.Error("session locked")
			} else {
				session.mu.Unlock()
			}
			if !e.replacementMu.TryLock() {
				t.Error("replacement reservation held")
			} else {
				e.replacementMu.Unlock()
			}
		}
		if partial {
			p.beginError = failure
		} else {
			p.endError = failure
		}
		m, err := s.planForeignModification(op.Context(), &foreignQuerySource{Wrapper: p}, catalog.ForeignInsert, false)
		if err != nil {
			t.Fatal(err)
		}
		err = m.begin(op.Context(), s)
		if partial && !errors.Is(err, failure) {
			t.Fatal(err)
		}
		err = s.finishForeignModifications(err)
		if !errors.Is(err, failure) || p.begins != 1 || p.ends != 1 {
			t.Fatal(err, p.begins, p.ends)
		}
		if err := m.end(context.Background()); !errors.Is(err, p.endError) {
			t.Fatal(err)
		}
		_ = s.Finish(failure)
	}
}

func fdwUnitCopyVeto(t *testing.T) {
	e := New()
	provider := &fdwUnitLifecycle{fdwUnitSingle: fdwUnitSingle{fdwUnitInsert: &fdwUnitInsert{present: true}}, tx: &fdwUnitTransaction{}}
	def, err := ValidateForeignDataWrapper(ForeignDataWrapperDefinition{HandlerName: catalog.ForeignFunctionName{Schema: "public", Name: "unit_copy_handler"}, Wrapper: provider})
	if err != nil {
		t.Fatal(err)
	}
	unexpected := errors.New("codec must not run before terminal input acceptance")
	if err := e.InstallForeignDefinitions([]ForeignDataWrapperDefinition{def}, nil,
		func(context.Context, driver.Value, sqltype.Usage, sqltype.InputEnvironment) (sqltype.Value, error) {
			return sqltype.Value{}, unexpected
		},
		func(context.Context, sqltype.Value, sqltype.OutputEnvironment) (driver.Value, error) {
			return nil, unexpected
		},
	); err != nil {
		t.Fatal(err)
	}
	session := sessionStart(t, e, nil)
	for _, sql := range []string{"CREATE FOREIGN DATA WRAPPER unit_copy HANDLER unit_copy_handler", "CREATE SERVER unit_copy_server FOREIGN DATA WRAPPER unit_copy", "CREATE USER MAPPING FOR CURRENT_USER SERVER unit_copy_server", "CREATE FOREIGN TABLE unit_copy_target(v integer) SERVER unit_copy_server"} {
		sessionSQL(t, session, sql)
	}
	op := transactionOperation(t, session)
	defer op.Done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("CopyFail-equivalent terminal input veto")
	defer scope.Finish(failure)
	syntax := &parser.Copy{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "unit_copy_target"}}}, From: true, Endpoint: parser.CopyStdin}
	count, err := scope.CopyProtocol(syntax, strings.NewReader("1\n"), nil, CopyProtocolHooks{FinishInput: func() error { return failure }})
	if !errors.Is(err, failure) || count != 0 || provider.calls != 0 || provider.begins != 0 || len(scope.owner.foreign.participants) != 0 {
		t.Fatal("input veto admitted remote work", count, err, provider.calls, provider.begins)
	}
	if err := scope.Finish(err); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
