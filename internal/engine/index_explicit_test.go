package engine

import (
	"context"
	"runtime"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/lock"
	"testing"
	"time"
)

func TestBtreeIndexExplicitExclusionBeforeTargets(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, target := range []string{"missing", "denied", "writer-blocked"} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+"/"+target, func(t *testing.T) {
				e := New()
				writer, builder := sessionStart(t, e, nil), sessionStart(t, e, nil)
				sessionSQL(t, writer, "CREATE TABLE t(n INT)")
				if target == "denied" {
					sessionSQL(t, writer, "CREATE USER reader")
					sessionSQL(t, builder, "SET ROLE reader")
				}
				if target == "writer-blocked" {
					tx := transactionBegin(t, writer)
					defer transactionEnd(t, writer, tx, false)
					sessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
				}
				tx := transactionBegin(t, builder)
				defer transactionEnd(t, builder, tx, false)
				query := "CREATE INDEX idx ON t(n)"
				if target == "missing" {
					query = "CREATE INDEX idx ON missing(n)"
				}
				if prepared {
					op, err := builder.Acquire(context.Background())
					if err != nil {
						t.Fatal(err)
					}
					_, err = e.Describe(op, mustLockStatement(query))
					op.Done()
					if err != nil {
						t.Fatal("syntax-only Prepare", err)
					}
				}
				ticket := builder.transactions.ticket
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				done := waitsCommandAsync(e, builder, ctx, query, false)
				seenShare := false
				for {
					lockState := e.locks.Inspect()
					for _, entry := range lockState.Resources {
						for _, request := range entry.Pending {
							if request.Ticket == ticket && request.Mode == lock.Share {
								seenShare = true
							}
						}
						if entry.Holders[ticket].Mode&lock.Share != 0 {
							seenShare = true
						}
					}
					if seenShare {
						cancel()
					}
					select {
					case err := <-done:
						if seenShare {
							t.Error("unsupported explicit CREATE INDEX requested Share")
						}
						waitsDiagnostic(t, err, "0A000", "statement is unsupported in an explicit transaction", "")
						if len(e.storage.IndexRoots) != 0 {
							t.Fatal("excluded utility published index roots")
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

func TestBtreeIndexOwnerBeforeWriterWait(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			e := New()
			writer, reader := sessionStart(t, e, nil), sessionStart(t, e, nil)
			sessionSQL(t, writer, "CREATE TABLE t(n INT)")
			sessionSQL(t, writer, "CREATE USER reader")
			ref := roleTableRef(t, e, "t")
			if err := e.SetObjectACL(context.Background(), ref, authorization.ACLValue{Explicit: true, Entries: []authorization.ACLEntry{{Grantor: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("postgres"), Grantee: authorization.ACLGrantee{Role: e.roles.published.Root.Read(authorization.PrincipalRef{}).LookupRef("reader")}, Privileges: authorization.SelectPrivilege | authorization.InsertPrivilege}}}); err != nil {
				t.Fatal(err)
			}
			sessionSQL(t, reader, "SET ROLE reader")
			tx := transactionBegin(t, writer)
			defer transactionEnd(t, writer, tx, false)
			sessionSQL(t, writer, "LOCK TABLE t IN ROW EXCLUSIVE MODE")
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
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			done := waitsCommandAsync(e, reader, ctx, query, false)
			seenShare := false
			for {
				lockState := e.locks.Inspect()
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
					waitsDiagnostic(t, err, "42501", "must be owner of table t", "")
					if len(e.storage.IndexRoots) != 0 {
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
