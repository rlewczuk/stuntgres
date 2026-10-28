package sqltype

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"stuntgres/internal/workadmission"
)

// These package-function witnesses supplement the public SQL lifecycle tests.
func scalarAdmissionCases(t *testing.T) []struct {
	name  string
	class workadmission.Class
	run   func(context.Context) (string, error)
	want  string
} {
	t.Helper()
	d := testTypes()
	normalize, err := BindLikeEscape(d[Text].Usage(), d[Text].Usage(), d[Text].Usage())
	if err != nil {
		t.Fatal(err)
	}
	return []struct {
		name  string
		class workadmission.Class
		run   func(context.Context) (string, error)
		want  string
	}{
		{"normalize", workadmission.WorkPatternNormalize, func(ctx context.Context) (string, error) {
			v, err := normalize.Evaluate(ctx, String(d[Text], "!a"), String(d[Text], "!"))
			return v.text, err
		}, "\\a"},
		{"match", workadmission.WorkPatternMatch, func(ctx context.Context) (string, error) {
			v, err := matchLike(ctx, "abc", "%c", false, false)
			if err != nil {
				return "", err
			}
			return strconv.FormatBool(v), nil
		}, "true"},
		{"calendar", workadmission.WorkCalendarArithmetic, func(ctx context.Context) (string, error) {
			p, err := BindArithmetic(ArithmeticAdd, d[Timestamp], d[Interval], d[Timestamp])
			if err != nil {
				return "", err
			}
			v, err := p.Binary(ctx, Value{typ: d[Timestamp]}, Value{typ: d[Interval], months: 1}, InputEnvironment{})
			if err != nil {
				if v != (Value{}) {
					t.Error("calendar rejection published value")
				}
				return "", err
			}
			return strconv.FormatInt(v.integer, 10), nil
		}, "2678400000000"},
		{"encoding", workadmission.WorkEncodingConvert, func(ctx context.Context) (string, error) {
			return ConvertBinaryEncoding(ctx, "\xe9", "LATIN1", "UTF8")
		}, "é"},
		{"bcrypt", workadmission.WorkPasswordBcrypt, func(ctx context.Context) (string, error) {
			return pgcryptoBcrypt(ctx, "U*U", "$2a$05$CCCCCCCCCCCCCCCCCCCCC.")
		}, "$2a$05$CCCCCCCCCCCCCCCCCCCCC.E5YPO9kmyuRGyh0XouQYb4YMJKvyOeW"},
		{"sha", workadmission.WorkPasswordSHA, func(ctx context.Context) (string, error) {
			return pgcryptoSHA(ctx, nil, "Hello world!", "$5$saltstring")
		}, "$5$saltstring$5B8vYYiY.CVt1RlTTf8KbXBH3hsxY/GNooZaBBGWEc5"},
	}
}

func TestScalarWorkAdmissionRefusalAndWrongClass(t *testing.T) {
	refused := errors.New("credits refused")
	for _, tc := range scalarAdmissionCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			policy := func(_ context.Context, class workadmission.Class) error {
				if class == tc.class {
					return refused
				}
				return nil
			}
			inert := workadmission.With(context.Background(), policy)
			if got, err := tc.run(inert); err != nil || got != tc.want {
				t.Fatalf("inert policy: %q %v", got, err)
			}
			if got, err := tc.run(workadmission.Activate(inert)); err != refused || got != "" {
				t.Fatalf("refusal: %q %v", got, err)
			}
			wrong := workadmission.WorkPatternNormalize
			if wrong == tc.class {
				wrong = workadmission.WorkPatternMatch
			}
			ctx := workadmission.Activate(workadmission.With(context.Background(), func(_ context.Context, class workadmission.Class) error {
				if class == wrong {
					return refused
				}
				return nil
			}))
			if got, err := tc.run(ctx); err != nil || got != tc.want {
				t.Fatalf("wrong class affected provider: %q %v", got, err)
			}
		})
	}
}

func TestScalarWorkAdmissionControlledCancellation(t *testing.T) {
	for _, tc := range scalarAdmissionCases(t) {
		t.Run(tc.name, func(t *testing.T) {
			parent, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			deadline, stop := context.WithTimeout(parent, 5*time.Second)
			defer stop()
			entered := make(chan struct{})
			ctx := workadmission.Activate(workadmission.With(deadline, func(ctx context.Context, class workadmission.Class) error {
				if class != tc.class {
					return nil
				}
				close(entered)
				<-ctx.Done()
				return ctx.Err()
			}))
			type result struct {
				value string
				err   error
			}
			done := make(chan result, 1)
			go func() { value, err := tc.run(ctx); done <- result{value, err} }()
			select {
			case <-entered:
			case result := <-done:
				t.Fatalf("provider finished before admission: %#v", result)
			case <-deadline.Done():
				cancel(nil)
				<-done
				t.Fatal("provider never entered admission")
			}
			cause := errors.New("caller cause")
			cancel(cause)
			outcome := <-done
			if outcome.err != context.Canceled || outcome.value != "" || context.Cause(ctx) != cause {
				t.Fatalf("cancellation: %#v cause %v", outcome, context.Cause(ctx))
			}
		})
	}
}

func TestScalarWorkAdmissionNoWorkAndLazyPrecedence(t *testing.T) {
	d := testTypes()
	refused := errors.New("unexpected admission")
	ctx := workadmission.Activate(workadmission.With(context.Background(), func(context.Context, workadmission.Class) error { return refused }))
	normalize, err := BindLikeEscape(d[Text].Usage(), d[Text].Usage(), d[Text].Usage())
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]Value{
		{Null(d[Text]), String(d[Text], "!")},
		{String(d[Text], "abc"), String(d[Text], "\\")},
		{String(d[Text], ""), String(d[Text], "!")},
	} {
		if _, err := normalize.Evaluate(ctx, pair[0], pair[1]); err != nil {
			t.Fatal("no-work normalization", err)
		}
	}
	if _, err := normalize.Evaluate(ctx, String(d[Text], "abc"), String(d[Text], "xx")); err == nil || err == refused {
		t.Fatal("invalid escape precedence", err)
	}
	if got, err := matchLike(ctx, "", "\\", false, false); err != nil || got {
		t.Fatal("lazy invalid pattern precedence", got, err)
	}
	match, err := BindLike("~~", d[Text].Usage(), d[Text].Usage(), d[Bool].Usage(), false)
	if err != nil {
		t.Fatal(err)
	}
	if v, err := match.Evaluate(ctx, Null(d[Text]), String(d[Text], "\\")); err != nil || !v.IsNull() {
		t.Fatal("strict matching bypass", v, err)
	}
	calendar, err := BindArithmetic(ArithmeticAdd, d[Timestamp], d[Interval], d[Timestamp])
	if err != nil {
		t.Fatal(err)
	}
	if v, err := calendar.Binary(ctx, Null(d[Timestamp]), Value{typ: d[Interval], months: 1}, InputEnvironment{}); err != nil || !v.IsNull() {
		t.Fatal("strict calendar bypass", v, err)
	}
	for _, interval := range []Value{{typ: d[Interval]}, {typ: d[Interval], integer: 1}} {
		if _, err := addTimestampInterval(ctx, 0, interval, d[Timestamp], false, InputEnvironment{}); err != nil {
			t.Fatal("noncalendar interval", err)
		}
	}
	if _, err := addTimestampInterval(ctx, TimestampPositiveInfinity, Value{typ: d[Interval], months: 1}, d[Timestamp], false, InputEnvironment{}); err != nil {
		t.Fatal("infinity before calendar admission", err)
	}
	if got, err := ConvertBinaryEncoding(ctx, "abc", "UTF8", "UTF8"); err != nil || got != "abc" {
		t.Fatal("identity conversion", got, err)
	}
	if _, err := ConvertBinaryEncoding(ctx, "abc", "INVALID", "UTF8"); err == nil || err == refused {
		t.Fatal("encoding name precedence", err)
	}
	if _, err := pgcryptoBcrypt(ctx, "x", "$2a$03$......................"); err == nil || err == refused {
		t.Fatal("bcrypt salt precedence", err)
	}
	if _, err := pgcryptoSHA(ctx, nil, "x", "$5$rounds=x$salt"); err == nil || err == refused {
		t.Fatal("SHA setting precedence", err)
	}
	if _, err := binaryDigest(ctx, "sha256", d[Bytea], "abc"); err != nil {
		t.Fatal("ordinary digest charged password admission", err)
	}
}
