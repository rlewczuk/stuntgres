package indexcmds_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
)

func TestBtreeIndexOwnerBeforeWriterWait(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, mode := range []string{"implicit", "explicit", "read-only"} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+"/"+mode, func(t *testing.T) {
				e := engine.New()
				writer, reader := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
				enginetest.SessionSQL(t, writer, "CREATE TABLE t(n INT)")
				enginetest.SessionSQL(t, writer, "CREATE USER reader")
				ref := enginetest.RoleTableRef(t, e, "t")
				if err := enginetest.SetObjectACL(e, context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: e.RoleStore.Published.Root.Read(authorization.PrincipalRef{}).LookupRef("reader")}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
					t.Fatal(err)
				}
				enginetest.SessionSQL(t, reader, "SET ROLE reader")
				tx := enginetest.TransactionBegin(t, writer)
				defer enginetest.TransactionEnd(t, writer, tx, false)
				enginetest.SessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
				const query = "CREATE INDEX forbidden ON t(n)"
				if prepared {
					op, err := reader.Acquire(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					_, err = e.Describe(op, mustLockStatement(query))
					op.Done()
					if err != nil {
						t.Fatal(err)
					}
				}
				if mode != "implicit" {
					rt := enginetest.TransactionBegin(t, reader)
					defer enginetest.TransactionEnd(t, reader, rt, false)
					if mode == "read-only" {
						enginetest.SessionSQL(t, reader, "SET TRANSACTION READ ONLY")
					}
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				done := waitsCommandAsync(e, reader, ctx, query, false)
				seenShare := false
				for {
					lockState := e.Locks.Inspect()
					for _, entry := range lockState.Resources {
						for _, request := range entry.Pending {
							if request.Mode == lock.Share {
								seenShare = true
							}
						}
					}
					if seenShare {
						cancel()
					}
					select {
					case err := <-done:
						if seenShare {
							t.Error("nonowner requested Share before ownership rejection")
						}
						if err == nil {
							t.Fatal("unauthorized/read-only build succeeded")
						}
						if len(e.Storage.IndexRoots) != 0 {
							t.Fatal("owner rejection published an index")
						}
						return
					default:
						runtime.Gosched()
					}
				}
			})
		}
	}
}

func mustLockStatement(q string) parser.Statement {
	statements, err := parser.Parse(q)
	if err != nil {
		panic(err)
	}
	return statements[0]
}

func waitsCommandAsync(e *core.Engine, s *session.Session, ctx context.Context, q string, prepare bool) <-chan error {
	done := make(chan error, 1)
	go func() {
		op, _, err := s.AcquireCommand(ctx)
		if err == nil {
			if prepare {
				_, err = e.Describe(op, mustLockStatement(q))
			} else {
				var r core.Result
				r, err = e.Execute(op, mustLockStatement(q), 0, nil)
				if r.Rows != nil {
					r.Rows.Close()
				}
			}
			err = op.CommandError(err)
			op.Done()
		}
		done <- err
	}()
	return done
}
