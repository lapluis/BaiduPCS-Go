package converter

import (
	"errors"
	"math"
	"reflect"
	"strconv"
	"testing"
)

func TestParsePositiveInt64ListRejectsWholeInvalidInput(t *testing.T) {
	for i, values := range [][]string{nil, {}, {""}, {"0"}, {"-1"}, {"9223372036854775808"}, {"bad"}, {"101", "bad"}, {"bad", "101"}, {" 7"}, {"0x7"}, {"1_0"}, {"7.0"}, {"７"}, {"+0"}} {
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			got, err := ParsePositiveInt64List(values)
			if err == nil || got != nil {
				t.Fatalf("invalid input returned partial selection: values=%q ids=%v err=%v", values, got, err)
			}
		})
	}
	for _, tc := range []struct {
		value string
		cause error
	}{{"bad", strconv.ErrSyntax}, {"9223372036854775808", strconv.ErrRange}} {
		_, err := ParsePositiveInt64List([]string{"101", tc.value})
		var number *strconv.NumError
		if !errors.As(err, &number) || !errors.Is(err, tc.cause) {
			t.Errorf("parse cause lost: %v", err)
		}
	}
}

func TestParsePositiveInt64ListPreservesValidForms(t *testing.T) {
	values := []string{"202", "+7", "007", "7", "9223372036854775807"}
	before := append([]string(nil), values...)
	got, err := ParsePositiveInt64List(values)
	want := []int64{202, 7, 7, 7, math.MaxInt64}
	if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(values, before) {
		t.Fatalf("valid ID forms changed: ids=%v input=%q err=%v", got, values, err)
	}
}

func TestLegacyInt64ConversionRemainsPermissive(t *testing.T) {
	got := SliceStringToInt64([]string{"7", "bad", "0", "-1", "+7", "007"})
	if want := []int64{7, 0, -1, 7, 7}; !reflect.DeepEqual(got, want) {
		t.Fatalf("legacy conversion changed: %v", got)
	}
}
