package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestDefaultTemporalConstructorAndRestoreCapture(t *testing.T) {
	e := New()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	a := sqltype.InputEnvironment{TransactionStart: time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC)}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	stage := e.BeginCatalogStage(auth.Catalog)
	err = e.schemaTableLocked(op, auth, stage, statement(t, `CREATE TABLE captured(ts TIMESTAMP DEFAULT 'now',tm TIME DEFAULT 'now')`).(*parser.CreateTable), ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, auth.State.CurrentUser, a, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := e.ValidateCatalogStage(context.Background(), stage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	if err = e.Dump(context.Background(), &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "'now'") || !strings.Contains(dump.String(), "2020-02-03 04:05:06") {
		t.Fatal("relative alias not captured", dump.String())
	}
	check := func(engine *Engine) {
		t.Helper()
		session := sessionStart(t, engine, nil)
		sessionSQL(t, session, `BEGIN`)
		session.transactions.mu.Lock()
		session.transactions.start = time.Date(2030, 7, 8, 9, 10, 11, 0, time.UTC)
		session.transactions.mu.Unlock()
		sessionSQL(t, session, `INSERT INTO captured DEFAULT VALUES`)
		r := sessionSQL(t, session, `SELECT * FROM captured`)
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
		sessionSQL(t, session, `COMMIT`)
	}
	// Later INSERT/Restore operations use their own transaction clocks, but the
	// definition must retain A rather than input decoding relative aliases again.
	check(e)
	restored := New()
	if err = restored.Restore(context.Background(), bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	check(restored)
}
