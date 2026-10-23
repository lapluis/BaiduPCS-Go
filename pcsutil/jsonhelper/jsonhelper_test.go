package jsonhelper

import (
	"strings"
	"testing"
)

func TestUnmarshalDataRequiresSingleDocument(t *testing.T) {
	for _, input := range []string{`{"value":1} {"value":2}`, `{"value":1} garbage`, `{"value":1} null`, `{"value":1} 42`} {
		var got map[string]int
		if err := UnmarshalData(strings.NewReader(input), &got); err == nil {
			t.Errorf("trailing data accepted: %q", input)
		}
	}
	var got map[string]int
	if err := UnmarshalData(strings.NewReader("{\"value\":1} \r\n\t"), &got); err != nil || got["value"] != 1 {
		t.Fatalf("valid document failed: %v", err)
	}
}
