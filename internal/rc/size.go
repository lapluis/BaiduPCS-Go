package rc

import (
	"errors"
	"math"
)

func addFileSize(total, size int64) (int64, error) {
	if size < 0 {
		return 0, errors.New("negative file size")
	}
	if size > math.MaxInt64-total {
		return 0, errors.New("total file size exceeds int64 limit")
	}
	return total + size, nil
}

// InventorySize counts regular file entries and sums their sizes exactly.
// Directories do not contribute; an unrepresentable total returns no partial result.
func InventorySize(entries []Entry) (count, total int64, err error) {
	for _, entry := range entries {
		if entry.IsDir {
			continue
		}
		total, err = addFileSize(total, entry.Size)
		if err != nil {
			return 0, 0, err
		}
		count++
	}
	return count, total, nil
}

func validatePlannedBytes(actions []Action) error {
	var total int64
	for _, action := range actions {
		if (action.Kind == "copy" || action.Kind == "skip") && action.Source != nil {
			var err error
			total, err = addFileSize(total, action.Source.Size)
			if err != nil {
				return err
			}
		}
	}
	return nil
}
