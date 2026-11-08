package converter

import (
	"fmt"
	"strconv"
)

// ParsePositiveInt64List validates the whole list before returning any IDs.
// Decimal spelling, order and repeated numeric IDs retain legacy semantics.
func ParsePositiveInt64List(values []string) ([]int64, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("ID 列表不能为空")
	}
	ids := make([]int64, 0, len(values))
	for i, value := range values {
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("第 %d 项 ID %q 无效: %w", i+1, value, err)
		}
		if id <= 0 {
			return nil, fmt.Errorf("第 %d 项 ID %q 必须为正整数", i+1, value)
		}
		ids = append(ids, id)
	}
	return ids, nil
}
