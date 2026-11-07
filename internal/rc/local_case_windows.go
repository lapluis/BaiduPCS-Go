package rc

import (
	"fmt"
	"path"
	"runtime"
	"sort"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var compareStringOrdinalProc = windows.NewLazySystemDLL("kernel32.dll").NewProc("CompareStringOrdinal")

// Windows ordinal comparison uses the OS uppercase table, which differs from
// Unicode lowercasing for names such as Latin k and the Kelvin sign.
// https://learn.microsoft.com/windows/win32/intl/handling-sorting-in-your-applications
func compareWindowsNames(a, b []uint16) (int, error) {
	result, _, err := compareStringOrdinalProc.Call(
		uintptr(unsafe.Pointer(&a[0])), uintptr(len(a)-1),
		uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)-1), 1)
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	if result == 0 {
		return 0, fmt.Errorf("compare Windows file names: %w", err)
	}
	return int(result) - 2, nil
}

func localPathsOverlap(a, b string) (bool, error) {
	if err := compareStringOrdinalProc.Find(); err != nil {
		return false, err
	}
	x, y := strings.Split(a, "/"), strings.Split(b, "/")
	for i := 0; i < min(len(x), len(y)); i++ {
		xName, err := windows.UTF16FromString(x[i])
		if err != nil {
			return false, err
		}
		yName, err := windows.UTF16FromString(y[i])
		if err != nil {
			return false, err
		}
		order, err := compareWindowsNames(xName, yName)
		if err != nil || order != 0 {
			return false, err
		}
	}
	return true, nil
}

// Check selected names and necessary parents against all existing destination
// names, including excluded entries, before any writes. Sort by the same OS
// ordinal comparison instead of grouping by Unicode lowercase strings.
func validateSelectedLocalPaths(source, destination map[string]Entry, o Options) error {
	selected := map[string]bool{}
	for name, entry := range source {
		if !o.Filter.Match(name) || (entry.IsDir && !o.CreateEmptyDirs) {
			continue
		}
		for p := name; p != "."; p = path.Dir(p) {
			selected[p] = true
		}
	}
	if len(selected) == 0 {
		return nil
	}
	for name := range selected {
		if err := validLocalName(name); err != nil {
			return err
		}
	}
	if err := compareStringOrdinalProc.Find(); err != nil {
		return err
	}
	all := make(map[string]bool, len(selected)+len(destination))
	for name := range destination {
		all[name] = false
	}
	for name := range selected {
		all[name] = true
	}
	type caseName struct {
		name     string
		units    []uint16
		selected bool
	}
	names := make([]caseName, 0, len(all))
	for name, selected := range all {
		units, err := windows.UTF16FromString(name)
		if err != nil {
			return err
		}
		names = append(names, caseName{name, units, selected})
	}
	var compareErr error
	sort.Slice(names, func(i, j int) bool {
		if compareErr != nil {
			return names[i].name < names[j].name
		}
		order, err := compareWindowsNames(names[i].units, names[j].units)
		if err != nil {
			compareErr = err
			return names[i].name < names[j].name
		}
		return order < 0 || order == 0 && names[i].name < names[j].name
	})
	if compareErr != nil {
		return compareErr
	}
	for i := 1; i < len(names); i++ {
		order, err := compareWindowsNames(names[i-1].units, names[i].units)
		if err != nil {
			return err
		}
		if order == 0 && (names[i-1].selected || names[i].selected) {
			return fmt.Errorf("case collision: %s and %s", names[i-1].name, names[i].name)
		}
	}
	return nil
}
