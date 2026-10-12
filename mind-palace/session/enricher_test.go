package session

import (
	"context"
	"reflect"
	"testing"
)

// isolateRegistry gives a test an empty registry and restores the original.
func isolateRegistry(t *testing.T) {
	t.Helper()
	saved := registry
	registry = map[string]Enricher{}
	t.Cleanup(func() { registry = saved })
}

func named(name string, enabled bool) Enricher {
	return Enricher{
		Name:       name,
		Collection: "hook_events",
		Enabled:    enabled,
		Matches:    func(Doc) bool { return true },
		Enrich:     func(context.Context, Doc, Lookup) (any, error) { return nil, nil },
	}
}

func names(es []Enricher) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.Name)
	}
	return out
}

func TestActiveReturnsEnabledEnrichers(t *testing.T) {
	isolateRegistry(t)
	Register(named("foo", true))
	if got := names(Active(nil)); !reflect.DeepEqual(got, []string{"foo"}) {
		t.Fatalf("Active = %v", got)
	}
}

func TestActiveSkipsDisabledEnrichers(t *testing.T) {
	isolateRegistry(t)
	Register(named("bar", false))
	if got := Active(nil); len(got) != 0 {
		t.Fatalf("Active = %v, want none", names(got))
	}
}

func TestActiveSkipsNamesInDisabledList(t *testing.T) {
	isolateRegistry(t)
	Register(named("baz", true))
	Register(named("keep", true))
	if got := names(Active([]string{"baz"})); !reflect.DeepEqual(got, []string{"keep"}) {
		t.Fatalf("Active = %v", got)
	}
}

func TestActiveIsEmptyWithNothingRegistered(t *testing.T) {
	isolateRegistry(t)
	if got := Active(nil); len(got) != 0 {
		t.Fatalf("Active = %v, want empty", names(got))
	}
}

func TestActiveIsSortedByNameAndFresh(t *testing.T) {
	isolateRegistry(t)
	Register(named("zeta", true))
	Register(named("alpha", true))
	Register(named("mid", true))
	got := Active(nil)
	if want := []string{"alpha", "mid", "zeta"}; !reflect.DeepEqual(names(got), want) {
		t.Fatalf("Active = %v, want %v", names(got), want)
	}
	got[0] = named("clobbered", true)
	if again := names(Active(nil)); again[0] != "alpha" {
		t.Fatalf("mutating the result changed the registry: %v", again)
	}
}

func TestRegisterPanicsOnDuplicateName(t *testing.T) {
	isolateRegistry(t)
	Register(named("dup", true))
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on duplicate name")
		}
	}()
	Register(named("dup", true))
}
