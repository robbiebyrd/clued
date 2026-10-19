package store_test

import (
	"context"
	"testing"

	"github.com/robbiebyrd/clued/mind-palace/kind"
	"github.com/robbiebyrd/clued/mind-palace/model"
	"github.com/robbiebyrd/clued/mind-palace/store"
	"github.com/robbiebyrd/clued/mind-palace/store/memstore"
)

const (
	tOld = "2026-10-01T10:00:00.000Z"
	tNew = "2026-10-02T10:00:00.000Z"
)

func stamped(d *model.Document, updated, content string) *model.Document {
	d.FrontMatter.Updated = updated
	d.Content = content
	return d
}

func TestParseConflictModeNewer(t *testing.T) {
	for _, s := range []string{"newer", "latest"} {
		m, err := store.ParseConflictMode(s)
		if err != nil || m != store.ConflictNewer {
			t.Errorf("ParseConflictMode(%q) = %q, %v", s, m, err)
		}
	}
}

// TestSyncNewer covers the same, an older and a newer source timestamp for
// both kinds and for templates, plus timestamps that cannot be compared.
func TestSyncNewer(t *testing.T) {
	ctx := context.Background()
	src, dst := memstore.New("src"), memstore.New("dst")
	type pair struct {
		id                 string
		srcUpdated, dstUpd string
	}
	cases := []pair{
		{"0001-sam", tOld, tOld},                        // same: source wins
		{"0002-new", tNew, tOld},                        // source later: source wins
		{"0003-old", tOld, tNew},                        // source older: conflict
		{"0004-fmt", "2026-10-02T12:00:00+02:00", tOld}, // other RFC 3339 form, later
		{"0005-non", "", tOld},                          // source missing: conflict
		{"0006-bad", tNew, "yesterday"},                 // target unparsable: conflict
	}
	for _, c := range cases {
		for _, mk := range []func(string) *model.Document{plan, story} {
			src.Put(ctx, stamped(mk(c.id), c.srcUpdated, "# source\n"))
			dst.Put(ctx, stamped(mk(c.id), c.dstUpd, "# target\n"))
		}
	}
	// Identical copies stay unchanged; missing ones are copied regardless of time.
	src.Put(ctx, stamped(plan("0007-equ"), tOld, "# same\n"))
	dst.Put(ctx, stamped(plan("0007-equ"), tOld, "# same\n"))
	src.Put(ctx, stamped(story("0008-cpy"), tOld, "# copy\n"))
	dst.Put(ctx, stamped(story("0009-ext"), tNew, "# extra\n"))

	for _, tm := range []struct{ id, srcUpd, dstUpd string }{
		{"same", tOld, tOld}, {"newer", tNew, tOld}, {"older", tOld, tNew},
	} {
		for _, k := range []string{kind.Plan, kind.Story} {
			src.PutTemplate(ctx, &model.Template{Kind: k, ID: tm.id, Content: "source", Updated: tm.srcUpd})
			dst.PutTemplate(ctx, &model.Template{Kind: k, ID: tm.id, Content: "target", Updated: tm.dstUpd})
		}
	}

	rep, err := store.Sync(ctx, src, dst, store.ConflictNewer)
	if err != nil {
		t.Fatalf("newer sync: %v", err)
	}
	if rep.Mode != "newer" {
		t.Errorf("mode = %q", rep.Mode)
	}
	wantOverwritten := []string{"0001-sam", "0002-new", "0004-fmt"}
	wantConflicts := []string{"0003-old", "0005-non", "0006-bad"}
	for _, k := range []string{kind.Plan, kind.Story} {
		ks := rep.Documents[k]
		if !equal(ks.Overwritten, wantOverwritten) {
			t.Errorf("%s overwritten = %v, want %v", k, ks.Overwritten, wantOverwritten)
		}
		if !equal(ks.Conflicts, wantConflicts) {
			t.Errorf("%s conflicts = %v, want %v", k, ks.Conflicts, wantConflicts)
		}
		for _, id := range wantOverwritten {
			if got, _ := dst.Get(ctx, k, id); got == nil || got.Content != "# source\n" {
				t.Errorf("%s %s not replaced: %+v", k, id, got)
			}
		}
		for _, id := range wantConflicts {
			if got, _ := dst.Get(ctx, k, id); got == nil || got.Content != "# target\n" {
				t.Errorf("%s %s overwritten despite a newer or unknown target: %+v", k, id, got)
			}
		}
	}
	if got := rep.Documents[kind.Plan].Unchanged; !equal(got, []string{"0007-equ"}) {
		t.Errorf("unchanged = %v", got)
	}
	if got := rep.Documents[kind.Story].Copied; !equal(got, []string{"0008-cpy"}) {
		t.Errorf("copied = %v", got)
	}
	if _, err := dst.Get(ctx, kind.Story, "0009-ext"); err != nil {
		t.Error("target-only story should be kept")
	}
	if got := len(rep.Conflicts()); got != 6 {
		t.Errorf("Conflicts() = %v", rep.Conflicts())
	}

	wantTmpl := map[string]bool{"plan/same": true, "plan/newer": true, "story/same": true, "story/newer": true}
	if len(rep.Templates) != len(wantTmpl) {
		t.Errorf("templates = %v", rep.Templates)
	}
	for _, l := range rep.Templates {
		if !wantTmpl[l] {
			t.Errorf("template %s replaced", l)
		}
	}
	if !equal(rep.TemplateConfl, []string{"plan/older", "story/older"}) {
		t.Errorf("template conflicts = %v", rep.TemplateConfl)
	}
	for _, k := range []string{kind.Plan, kind.Story} {
		for id, want := range map[string]string{"same": "source", "newer": "source", "older": "target"} {
			got, err := dst.GetTemplate(ctx, k, id)
			if err != nil || got.Content != want {
				t.Errorf("template %s/%s = %+v, %v; want %q", k, id, got, err, want)
			}
		}
	}

	// A second run finds the replaced copies unchanged and the conflicts again.
	rep, err = store.Sync(ctx, src, dst, store.ConflictNewer)
	if err != nil {
		t.Fatal(err)
	}
	if ks := rep.Documents[kind.Plan]; len(ks.Overwritten) != 0 || !equal(ks.Conflicts, wantConflicts) || len(ks.Unchanged) != 4 {
		t.Errorf("second run: %+v", ks)
	}
	if len(rep.Templates) != 0 || len(rep.TemplateConfl) != 2 {
		t.Errorf("second run templates: %v %v", rep.Templates, rep.TemplateConfl)
	}
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
