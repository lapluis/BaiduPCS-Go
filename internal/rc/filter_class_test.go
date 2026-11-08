package rc

import (
	"context"
	"os"
	"testing"
)

func TestFilterClassesStayWithinPathComponent(t *testing.T) {
	for _, tc := range []struct {
		pattern, name string
		want          bool
	}{
		{"a[!x]b", "a/b", false},
		{"a[^x]b", "a/b", false},
		{"a[.-0]b", "a/b", false},
		{"a[/]b", "a/b", false},
		{`a[\x2f]b`, "a/b", false},
		{`a[\s\S]b`, "a/b", false},
		{"a[^\n]b", "a/b", false},
		{"a[!x]b", "aYb", true},
		{"a[!x]b", "axb", false},
		{"a[!x]b", "a\nb", true},
		{"a[.-0]b", "a.b", true},
		{"a[.-0]b", "a0b", true},
		{"a[/Y]b", "aYb", true},
		{`a[\s\S]b`, "a\nb", true},
		{"a[^\n]b", "aYb", true},
		{"a[^\n]b", "a\nb", false},
		{"[α-ω].txt", "λ.txt", true},
		{"[Aa].txt", "a.txt", true},
		{"[Aa].txt", "A.txt", true},
		{"[KkK].txt", "K.txt", true},
		{"[Σσς].txt", "ς.txt", true},
	} {
		t.Run(tc.pattern+"/"+tc.name, func(t *testing.T) {
			f, err := NewFilter([]string{tc.pattern}, nil, "")
			if err != nil || f.Match(tc.name) != tc.want {
				t.Fatalf("include match=%t want=%t err=%v", f.Match(tc.name), tc.want, err)
			}
			f, err = NewFilter(nil, []string{tc.pattern}, "")
			if err != nil || f.Match(tc.name) == tc.want {
				t.Fatalf("exclude match=%t want=%t err=%v", f.Match(tc.name), !tc.want, err)
			}
		})
	}
}

func TestInvalidFilterClassesReturnErrors(t *testing.T) {
	for _, pattern := range []string{"[", "[]", "[z-a]", "[!"} {
		if _, err := NewFilter([]string{pattern}, nil, ""); err == nil {
			t.Errorf("invalid include accepted: %q", pattern)
		}
		if _, err := NewFilter(nil, []string{pattern}, ""); err == nil {
			t.Errorf("invalid exclude accepted: %q", pattern)
		}
	}
}

func TestSyncClassIncludeCannotSelectNestedFile(t *testing.T) {
	src, dst := endpoints(t)
	write(t, dst, "a/b", "keep nested file")
	write(t, dst, "aYb", "selected flat file")
	o := opts()
	var err error
	o.Filter, err = NewFilter([]string{"a[!x]b"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(context.Background(), src, dst, "sync", o)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range plan.Actions {
		if a.Kind == "delete" && a.Path != "aYb" {
			t.Fatalf("character class selected a nested file for deletion: %+v", a)
		}
	}
	result, err := Execute(context.Background(), plan, o)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if data, err := os.ReadFile(dst.At("a/b")); err != nil || string(data) != "keep nested file" {
		t.Fatalf("unselected nested file changed: %q %v", data, err)
	}
	if _, err := os.Stat(dst.At("aYb")); !os.IsNotExist(err) {
		t.Fatalf("selected file not removed: %v", err)
	}
}
