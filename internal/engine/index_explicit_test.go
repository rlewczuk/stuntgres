package engine

import (
	"context"
	"runtime"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/lock"
	"testing"
	"time"
)

func TestBtreeIndexOwnerBeforeWriterWait(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, mode := range []string{"implicit", "explicit", "read-only"} {
			t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared]+"/"+mode, func(t *testing.T) {
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
				if mode != "implicit" {
					rt := transactionBegin(t, reader)
					defer transactionEnd(t, reader, rt, false)
					if mode == "read-only" {
						sessionSQL(t, reader, "SET TRANSACTION READ ONLY")
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
						if err == nil {
							t.Fatal("unauthorized/read-only build succeeded")
						}
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
}
