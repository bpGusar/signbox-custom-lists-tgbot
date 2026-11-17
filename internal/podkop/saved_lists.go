package podkop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lst-signbox-lists-tgbot/internal/lists"
)

type SavedListFile struct {
	Path     string
	Type     lists.EntryType
	Active   int
	Disabled int
}

func SavedListFiles(dir string) ([]SavedListFile, error) {
	files, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []SavedListFile
	for _, file := range files {
		if !file.Type().IsRegular() || !strings.HasSuffix(file.Name(), ".lst") {
			continue
		}
		path := filepath.Join(dir, file.Name())
		if err := ValidatePath(path); err != nil {
			continue
		}
		entries, err := lists.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("не удалось прочитать %s: %w", path, err)
		}
		saved := SavedListFile{Path: path, Type: listTypeFromEntries(path, entries)}
		for _, entry := range entries {
			if entry.Disabled {
				saved.Disabled++
			} else {
				saved.Active++
			}
		}
		out = append(out, saved)
	}
	return out, nil
}
