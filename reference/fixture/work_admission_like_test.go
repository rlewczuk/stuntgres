package fixture

import (
	"context"
	"errors"
	"stuntgres"
	"testing"
	"time"
)

func TestReferenceWorkAdmissionGrantResume(t *testing.T) {
	p := NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			RunStep(t, p.Ctx, c, b.Recorder, Exec("CREATE TABLE admission_like(v TEXT,p TEXT,e TEXT)", 0))
			RunStep(t, p.Ctx, c, b.Recorder, Exec("INSERT INTO admission_like VALUES('a','!a','!')", 1))
			const query = "SELECT v LIKE p ESCAPE e AS matched FROM admission_like"
			held, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if !b.PG {
				for _, class := range []stuntgres.WorkClass{stuntgres.WorkPatternNormalize, stuntgres.WorkPatternMatch} {
					t.Run("native finite quantum", func(t *testing.T) {
						ctx, cancel := context.WithTimeout(p.Ctx, 5*time.Second)
						defer cancel()
						entered := make(chan struct{}, 1)
						credits := make(chan struct{}, 64)
						controlled := stuntgres.WithWorkAdmission(ctx, func(effective context.Context, actual stuntgres.WorkClass) error {
							if actual != class {
								return nil
							}
							select {
							case entered <- struct{}{}:
							default:
							}
							select {
							case <-credits:
								return nil
							case <-effective.Done():
								return effective.Err()
							}
						})
						value := make(chan bool, 1)
						done := admissionSQLWorker(t, controlled, func(ctx context.Context) RelationOutcome {
							var matched bool
							err := held.QueryRowContext(ctx).Scan(&matched)
							if err == nil {
								value <- matched
							}
							return RelationOutcome{Err: err}
						}, cancel)
						select {
						case <-entered:
						case out := <-done:
							t.Fatal("finite LIKE bypassed target work class", class, out.Err)
						case <-ctx.Done():
							t.Fatal("finite LIKE admission deadline", ctx.Err())
						}
						select {
						case matched := <-value:
							t.Fatal("withheld LIKE credit published result", matched)
						default:
						}
						for range cap(credits) {
							credits <- struct{}{}
						}
						if out := admissionSQLJoin(t, done); out.Err != nil {
							t.Fatal("granted LIKE", out.Err)
						}
						if !<-value {
							t.Fatal("finite granted LIKE must match")
						}
					})
				}
				refusal := errors.New("no work must not spend pattern credits")
				refuse := stuntgres.WithWorkAdmission(p.Ctx, func(context.Context, stuntgres.WorkClass) error { return refusal })
				RunStep(t, refuse, c, b.Recorder, Select("SELECT NULL::text LIKE p ESCAPE e AS matched FROM admission_like", []string{"matched"}, []string{"BOOL"}, [][]any{{nil}}))
			}
			RunStep(t, p.Ctx, HeldStatement{Executor: c, Statement: held, SQL: query}, b.Recorder, Select(query, []string{"matched"}, []string{"BOOL"}, [][]any{{true}}))
			RunStep(t, p.Ctx, c, b.Recorder, Exec("UPDATE admission_like SET p='%a'", 1))
			if !b.PG {
				wrong := stuntgres.WithWorkAdmission(p.Ctx, func(ctx context.Context, class stuntgres.WorkClass) error {
					if class == stuntgres.WorkPatternNormalize {
						return errors.New("matching consumed normalization credit")
					}
					return nil
				})
				RunStep(t, wrong, c, b.Recorder, Select("SELECT v LIKE p AS matched FROM admission_like", []string{"matched"}, []string{"BOOL"}, [][]any{{true}}))
			}
			RunStep(t, p.Ctx, c, b.Recorder, Select("SELECT v LIKE p AS matched FROM admission_like", []string{"matched"}, []string{"BOOL"}, [][]any{{true}}))
		})
	}
}
