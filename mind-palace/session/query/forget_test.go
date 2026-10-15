package query_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/robbiebyrd/clued/mind-palace/session"
	"github.com/robbiebyrd/clued/mind-palace/session/memsession"
	"github.com/robbiebyrd/clued/mind-palace/session/query"
)

func TestForgetLimitDeletesTheOldestFirst(t *testing.T) {
	st := memsession.New()
	seedSession(t, st, account, "s1", "/repo/a", "", t0)
	seedSession(t, st, account, "s2", "/repo/a", "", t0.Add(time.Hour))
	seedSession(t, st, account, "s3", "/repo/a", "", t0.Add(2*time.Hour))
	svc := &query.Service{Store: st, AccountID: account}
	args := query.ForgetArgs{Before: t0.Add(3 * time.Hour).Format(time.RFC3339), Limit: 2}
	got, err := svc.Forget(ctx, args)
	must(t, err)
	if want := (session.Forgotten{Sessions: []string{"s1", "s2"}, Documents: 2, More: true}); !reflect.DeepEqual(got, want) {
		t.Fatalf("first batch = %#v, want %#v", got, want)
	}
	got, err = svc.Forget(ctx, args)
	must(t, err)
	if want := (session.Forgotten{Sessions: []string{"s3"}, Documents: 1}); !reflect.DeepEqual(got, want) {
		t.Fatalf("second batch = %#v, want %#v", got, want)
	}
	if _, err := svc.Forget(ctx, query.ForgetArgs{Limit: 5}); !errors.Is(err, query.ErrNothingToForget) {
		t.Fatalf("a limit alone = %v, want ErrNothingToForget", err)
	}
}
