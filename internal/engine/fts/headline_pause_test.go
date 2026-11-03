package fts

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"

	"stuntgres/internal/util"
)

func TestFTSHeadlineExecutionPause(t *testing.T) {
	for _, tc := range []struct {
		name, point, text, query, options, want string
	}{
		{"selection", "text_search.headline.select", "cat rat", "cat & rat", "MaxFragments=1,MaxWords=2,MinWords=1", "<b>cat</b> <b>rat</b>"},
		{"render", "text_search.headline.render", "Cat", "cat", "HighlightAll=true", "<b>Cat</b>"},
	} {
		for _, canceled := range []bool{false, true} {
			name := "resume"
			if canceled {
				name = "cancel"
			}
			t.Run(tc.name+"/"+name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					document := headlineUnitDocument(t, tc.text)
					query, err := ParseQuery(t.Context(), tc.query)
					if err != nil {
						t.Fatal(err)
					}
					options, err := ParseHeadlineOptions(t.Context(), tc.options, headlineUnitInteger)
					if err != nil {
						t.Fatal(err)
					}
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					pause := util.NewExecutionPause(tc.point)
					defer pause.Resume()
					effective := util.ActivateExecutionPause(util.WithExecutionPause(ctx, pause))
					type outcome struct {
						text string
						err  error
					}
					done := make(chan outcome, 1)
					go func() {
						text, err := document.Headline(effective, query, options)
						done <- outcome{text, err}
					}()
					if err := pause.Wait(ctx); err != nil {
						t.Fatal(err)
					}
					synctest.Wait()
					select {
					case result := <-done:
						t.Fatal("paused headline published output", result)
					default:
					}
					if canceled {
						cancel()
					} else {
						pause.Resume()
					}
					result := <-done
					if canceled {
						if result.text != "" || !errors.Is(result.err, context.Canceled) {
							t.Fatal("canceled pause published partial output", result)
						}
					} else if result.err != nil || result.text != tc.want {
						t.Fatal("resumed headline changed bytes", result)
					}
				})
			})
		}
		t.Run(tc.name+"/empty", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				document := headlineUnitDocument(t, "")
				query, err := ParseQuery(t.Context(), "cat")
				if err != nil {
					t.Fatal(err)
				}
				options, err := ParseHeadlineOptions(t.Context(), tc.options, headlineUnitInteger)
				if err != nil {
					t.Fatal(err)
				}
				pause := util.NewExecutionPause(tc.point)
				defer pause.Resume()
				ctx := util.ActivateExecutionPause(util.WithExecutionPause(t.Context(), pause))
				// No selected fragment or emitted byte: no work-point admission.
				// A mistaken suspension here is a deterministic bubble deadlock.
				if got, err := document.Headline(ctx, query, options); err != nil || got != "" {
					t.Fatal("empty headline", got, err)
				}
			})
		})
	}
}
