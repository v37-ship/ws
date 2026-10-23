package service

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// MaxReceiptBytes is the transfer-proof upload limit (screenshots or bank PDFs).
const MaxReceiptBytes = 5 << 20

var allowedReceiptExts = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
	".pdf":  true,
}

// ValidateReceiptName validates a payment receipt upload and returns a
// collision-free storage name (the original name is never used on disk).
func ValidateReceiptName(original string, size int64) (string, error) {
	if size > MaxReceiptBytes {
		return "", fmt.Errorf("%w: receipt must be under 5 MB", ErrValidation)
	}
	ext := strings.ToLower(filepath.Ext(original))
	if !allowedReceiptExts[ext] {
		return "", fmt.Errorf("%w: receipt must be JPG, PNG, WebP or PDF", ErrValidation)
	}
	return uuid.NewString() + ext, nil
}
