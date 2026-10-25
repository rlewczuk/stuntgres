package tablecmds_test

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDefaultTemporalConstructorAndRestoreCapture(t *testing.T) {
	e := engine.New()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	a := sqltype.InputEnvironment{TransactionStart: time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC)}
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	err = engine.SchemaTableLocked(e, op, auth, stage, enginetest.Statement(t, `CREATE TABLE captured(ts TIMESTAMP DEFAULT 'now',tm TIME DEFAULT 'now')`).(*parser.CreateTable), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, a, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = enginetest.PublishCatalog(e, prepared, engine.PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err = engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "'now'") || !strings.Contains(dump.String(), "2020-02-03 04:05:06") {
		t.Fatal("relative alias not captured", dump.String())
	}
	check := func(eng *engine.Engine) {
		t.Helper()
		session := enginetest.StartSession(t, eng, nil)
		enginetest.SessionSQL(t, session, `BEGIN`)
		session.Transactions.Mu.Lock()
		session.Transactions.Start = time.Date(2030, 7, 8, 9, 10, 11, 0, time.UTC)
		session.Transactions.Mu.Unlock()
		enginetest.SessionSQL(t, session, `INSERT INTO captured DEFAULT VALUES`)
		r := enginetest.SessionSQL(t, session, `SELECT * FROM captured`)
		values := make([]sqltype.Value, 2)
		if present, err := r.Rows.Next(context.Background(), values); err != nil || !present {
			t.Fatal(present, err)
		}
		for i, want := range []string{"2020-02-03 04:05:06", "04:05:06"} {
			got, err := values[i].Type().Output(context.Background(), values[i])
			if err != nil || got != want {
				t.Fatal(got, want, err)
			}
		}
		enginetest.SessionSQL(t, session, `COMMIT`)
	}
	// Later INSERT/Restore operations use their own transaction clocks, but the
	// definition must retain A rather than input decoding relative aliases again.
	check(e)
	restored := engine.New()
	if err = engine.Restore(context.Background(), restored, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(restored)
}
