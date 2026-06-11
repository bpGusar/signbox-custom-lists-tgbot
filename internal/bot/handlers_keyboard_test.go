package bot

import "testing"

func keyboardTexts(t *testing.T, opID string, newVals, active, disabled []string) []string {
	t.Helper()
	rows, _ := buildListInputKeyboard(opID, newVals, active, disabled)
	var texts []string
	for _, row := range rows {
		for _, btn := range row {
			texts = append(texts, btn.Text)
		}
	}
	return texts
}

func containsAll(texts []string, want ...string) bool {
	set := make(map[string]struct{}, len(texts))
	for _, t := range texts {
		set[t] = struct{}{}
	}
	for _, w := range want {
		if _, ok := set[w]; !ok {
			return false
		}
	}
	return true
}

func containsNone(texts []string, forbidden ...string) bool {
	set := make(map[string]struct{}, len(texts))
	for _, t := range texts {
		set[t] = struct{}{}
	}
	for _, f := range forbidden {
		if _, ok := set[f]; ok {
			return false
		}
	}
	return true
}

func TestBuildListInputKeyboard_onlyNew(t *testing.T) {
	texts := keyboardTexts(t, "op1", []string{"test.com", "example.org"}, nil, nil)

	if !containsAll(texts, "➕ Добавить", "❌ Отмена") {
		t.Fatalf("expected add and cancel, got %v", texts)
	}
	if !containsNone(texts, "⏸ Отключить", "🗑 Удалить", "✅ Включить", "➕ Добавить всё") {
		t.Fatalf("unexpected extra buttons: %v", texts)
	}
}

func TestBuildListInputKeyboard_mixedBuckets(t *testing.T) {
	texts := keyboardTexts(t, "op2",
		[]string{"test.com", "example.org"},
		[]string{"revanced.app"},
		[]string{"kick.com"},
	)

	if !containsAll(texts,
		"➕ Добавить", "✅ Включить", "➕ Добавить всё",
		"🗑 Удалить", "⏸ Отключить", "❌ Отмена",
	) {
		t.Fatalf("expected full action set, got %v", texts)
	}
}

func TestBuildListInputKeyboard_onlyDisabled(t *testing.T) {
	texts := keyboardTexts(t, "op3", nil, nil, []string{"kick.com"})

	if !containsAll(texts, "✅ Включить", "🗑 Удалить", "❌ Отмена") {
		t.Fatalf("expected enable/delete/cancel, got %v", texts)
	}
	if !containsNone(texts, "➕ Добавить", "⏸ Отключить", "➕ Добавить всё") {
		t.Fatalf("unexpected buttons: %v", texts)
	}
}

func TestBuildListInputKeyboard_onlyActive(t *testing.T) {
	texts := keyboardTexts(t, "op4", nil, []string{"revanced.app"}, nil)

	if !containsAll(texts, "🗑 Удалить", "⏸ Отключить", "❌ Отмена") {
		t.Fatalf("expected delete/disable/cancel, got %v", texts)
	}
	if !containsNone(texts, "➕ Добавить", "✅ Включить", "➕ Добавить всё") {
		t.Fatalf("unexpected buttons: %v", texts)
	}
}
