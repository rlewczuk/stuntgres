package exec

import (
	"context"
	"errors"
	"testing"
	"time"

	"stuntgres/internal/sqltype"
	"stuntgres/internal/util"
)

func TestExecutionPauseWorkerContextTransport(t *testing.T) {
	type key struct{}
	incomingPause := util.NewExecutionPause("encoding.convert")
	defer incomingPause.Resume()
	incoming := util.ActivateExecutionPause(util.WithExecutionPause(context.WithValue(context.Background(), key{}, "source"), incomingPause))
	state := State{}
	for _, mode := range []string{"cancel", "resume"} {
		t.Run(mode, func(t *testing.T) {
			pause := util.NewExecutionPause("encoding.convert")
			defer pause.Resume()
			bounded, stop := context.WithTimeout(util.WithExecutionPause(context.WithValue(context.Background(), key{}, mode), pause), 5*time.Second)
			defer stop()
			worker, cancel := context.WithCancelCause(bounded)
			defer cancel(nil)
			// A new invocation explicitly supplies its own genuine effective child.
			state.ExecutionContext = worker
			evaluation := state.Evaluation(incoming)
			if evaluation.Context.Done() != worker.Done() || evaluation.Context.Value(key{}) != mode {
				t.Fatal("source replaced worker authority")
			}
			got, gok := evaluation.Context.Deadline()
			want, wok := worker.Deadline()
			if gok != wok || !got.Equal(want) {
				t.Fatal("worker deadline changed")
			}
			if allocations, _ := util.AllocationsPerCall(100, func() { evaluation.setContext(incoming) }); allocations != 0 {
				t.Fatal("worker pull reset allocated", allocations)
			}
			done := make(chan error, 1)
			go func() {
				_, err := sqltype.ConvertBinaryEncoding(evaluation.Context, "abc", "UTF8", "LATIN1")
				done <- err
			}()
			joined := false
			defer func() {
				pause.Resume()
				if !joined {
					<-done
				}
			}()
			if err := pause.Wait(bounded); err != nil {
				t.Fatal("actual selected worker provider", err)
			}
			cause := errors.New("selected worker stopped")
			if mode == "cancel" {
				cancel(cause)
			} else {
				pause.Resume()
			}
			err := <-done
			joined = true
			if mode == "cancel" {
				if err != context.Canceled || context.Cause(evaluation.Context) != cause || incoming.Err() != nil {
					t.Fatal("selected worker cancellation lost", err)
				}
			} else if err != nil {
				t.Fatal("fresh worker retained old cancellation or request", err)
			}
			stage := Stage{Evaluation: evaluation}
			if err := stage.Close(); err != nil {
				t.Fatal(err)
			}
			if stage.Evaluation.Context != nil || stage.Evaluation.Query != nil {
				t.Fatal("terminal stage retained worker invocation")
			}
		})
	}
	incomingPause.Resume()
	if err := incomingPause.Wait(context.Background()); err == nil {
		t.Fatal("incoming request replaced worker pause owner")
	}
}
