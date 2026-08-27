package lists

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// ValidateCategoryName normalises a user-supplied category name and rejects
// the ones that cannot survive a round-trip through the file format.
func ValidateCategoryName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("имя категории пустое")
	}
	if strings.ContainsAny(name, "\n\r\t") {
		return "", fmt.Errorf("имя категории не должно содержать переносы строк и табуляции")
	}
	if utf8.RuneCountInString(name) > MaxCategoryNameLen {
		return "", fmt.Errorf("имя категории длиннее %d символов", MaxCategoryNameLen)
	}
	if strings.HasPrefix(name, "#") || strings.HasPrefix(name, "/") {
		return "", fmt.Errorf("имя категории не должно начинаться с «#» или «/»")
	}
	if strings.EqualFold(name, UncategorizedLabel) {
		return "", fmt.Errorf("«%s» — служебное имя, выберите другое", UncategorizedLabel)
	}
	return name, nil
}
