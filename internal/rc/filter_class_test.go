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
		{"a[/Y]b", "dir/aYb", true},
		{`a[\x2fY]b`, "dir/aYb", true},
		{"/a[/Y]b", "dir/aYb", false},
		{"dir/a[/Y]b", "dir/aYb", true},
		{"dir/a[/Y]b", "other/dir/aYb", false},
		{"**/a[/Y]b", "other/dir/aYb", true},
		{`a[\s\S]b`, "a\nb", true},
		{"a[^\n]b", "aYb", true},
		{"a[^\n]b", "a\nb", false},
		{"[α-ω].txt", "λ.txt", true},
		{"[Aa].txt", "a.txt", true},
		{"[Aa].txt", "A.txt", true},
		{"[KkK].txt", "K.txt", true},
		{"[Σσς].txt", "ς.txt", true},
		{`a[\]]b`, "a]b", true},
		{`a[\]]b`, "aXb", false},
		{`a[!\]]b`, "a]b", false},
		{`a[!\]]b`, "aYb", true},
		{`a[!\]]b`, "a/b", false},
		{"a[[:digit:]]b", "a5b", true},
		{"a[[:digit:]]b", "aXb", false},
		{"a[![:digit:]]b", "aYb", true},
		{"a[![:digit:]]b", "a/b", false},
		{"a[[:punct:]]b", "a]b", true},
		{"a[[:punct:]]b", "a/b", false},
		{`a[\\]b`, `a\b`, true},
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

func TestSyncClassSlashDoesNotDisableNestedExclusion(t *testing.T) {
	src, dst := endpoints(t)
	for _, name := range []string{"aYb", "dir/aYb", "other/dir/aYb", "dir/aXb"} {
		write(t, dst, name, "fixture "+name)
	}
	o := opts()
	var err error
	o.Filter, err = NewFilter(nil, []string{"a[/Y]b"}, "")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildPlan(context.Background(), src, dst, "sync", o)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Execute(context.Background(), plan, o)
	if err != nil || result.Deleted != 1 {
		t.Errorf("result=%+v err=%v", result, err)
	}
	for _, name := range []string{"aYb", "dir/aYb", "other/dir/aYb"} {
		if data, err := os.ReadFile(dst.At(name)); err != nil || string(data) != "fixture "+name {
			t.Errorf("excluded file changed: %s data=%q err=%v", name, data, err)
		}
	}
	if _, err := os.Stat(dst.At("dir/aXb")); !os.IsNotExist(err) {
		t.Errorf("selected file not removed: %v", err)
	}
}

func TestInvalidFilterClassesReturnErrors(t *testing.T) {
	for _, pattern := range []string{"[", "[]", "[z-a]", "[!", `[\]`, "[[:digit:]", "[[:unknown:]]", "[[:digit]]"} {
		if _, err := NewFilter([]string{pattern}, nil, ""); err == nil {
			t.Errorf("invalid include accepted: %q", pattern)
		}
		if _, err := NewFilter(nil, []string{pattern}, ""); err == nil {
			t.Errorf("invalid exclude accepted: %q", pattern)
		}
	}
}

func TestSyncDelimitedClassesProtectUnselectedFiles(t *testing.T) {
	for _, tc := range []struct {
		name, include, exclude, selected, protected string
	}{
		{"escaped-include", `a[!\]]b`, "", "aYb", "a]b"},
		{"escaped-exclude", "", `a[\]]b`, "aYb", "a]b"},
		{"posix-include", "a[[:digit:]]b", "", "a5b", "aXb"},
		{"posix-exclude", "", "a[![:digit:]]b", "a5b", "aXb"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src, dst := endpoints(t)
			write(t, dst, tc.selected, "remove selected")
			write(t, dst, tc.protected, "keep unselected")
			o := opts()
			var include, exclude []string
			if tc.include != "" {
				include = []string{tc.include}
			}
			if tc.exclude != "" {
				exclude = []string{tc.exclude}
			}
			var err error
			o.Filter, err = NewFilter(include, exclude, "")
			if err != nil {
				t.Fatal(err)
			}
			plan, err := BuildPlan(context.Background(), src, dst, "sync", o)
			if err != nil {
				t.Fatal(err)
			}
			result, err := Execute(context.Background(), plan, o)
			if err != nil || result.Deleted != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if data, err := os.ReadFile(dst.At(tc.protected)); err != nil || string(data) != "keep unselected" {
				t.Fatalf("unselected file changed: %q %v", data, err)
			}
			if _, err := os.Stat(dst.At(tc.selected)); !os.IsNotExist(err) {
				t.Fatalf("selected file not removed: %v", err)
			}
		})
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
