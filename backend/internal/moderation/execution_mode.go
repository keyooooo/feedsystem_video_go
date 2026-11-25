package moderation

import (
	"fmt"
	"strings"
)

// AutoFoldMode is chosen by the operator, never by model input.
type AutoFoldMode string

const (
	AutoFoldShadow    AutoFoldMode = "shadow"
	AutoFoldThreshold AutoFoldMode = "threshold"
	AutoFoldApproved  AutoFoldMode = "approved"
)

func ParseAutoFoldMode(value string) (AutoFoldMode, error) {
	switch mode := AutoFoldMode(strings.TrimSpace(value)); mode {
	case "", AutoFoldApproved:
		return AutoFoldApproved, nil
	case AutoFoldShadow, AutoFoldThreshold:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid MODERATION_AUTO_FOLD_MODE: expected shadow, threshold or approved")
	}
}

func autoFoldMode(modes []AutoFoldMode) AutoFoldMode {
	if len(modes) == 0 {
		return AutoFoldApproved
	}
	if len(modes) != 1 {
		return AutoFoldShadow
	}
	switch modes[0] {
	case AutoFoldApproved, AutoFoldThreshold, AutoFoldShadow:
		return modes[0]
	default:
		return AutoFoldShadow
	}
}
