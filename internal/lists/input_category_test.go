package lists

import (
	"reflect"
	"testing"
)

func TestParseInputWithCategories(t *testing.T) {
	r := ParseInput("// #cat: xda\nxda-developers.com\nxdaforums.com\n// #cat: cursor\ncursor-cdn.com\ncursor.com\ncursor.sh")
	if r.Type != TypeDomain || len(r.Invalid) != 0 || r.Mixed {
		t.Fatalf("unexpected result %+v", r)
	}
	wantValid := []string{"xda-developers.com", "xdaforums.com", "cursor-cdn.com", "cursor.com", "cursor.sh"}
	if !reflect.DeepEqual(r.Valid, wantValid) {
		t.Fatalf("valid = %v", r.Valid)
	}
	wantCats := map[string]string{
		"xda-developers.com": "xda",
		"xdaforums.com":      "xda",
		"cursor-cdn.com":     "cursor",
		"cursor.com":         "cursor",
		"cursor.sh":          "cursor",
	}
	if !reflect.DeepEqual(r.Categories, wantCats) {
		t.Fatalf("categories = %v", r.Categories)
	}
}

func TestParseInputCategoriesIPAndUncat(t *testing.T) {
	r := ParseInput("1.1.1.1\n// #cat: dns\n8.8.8.8, 9.9.9.9\n// #uncat\n10.0.0.0/8")
	if r.Type != TypeIP || len(r.Invalid) != 0 {
		t.Fatalf("unexpected result %+v", r)
	}
	want := map[string]string{"1.1.1.1": "", "8.8.8.8": "dns", "9.9.9.9": "dns", "10.0.0.0/8": ""}
	if !reflect.DeepEqual(r.Categories, want) {
		t.Fatalf("categories = %v", r.Categories)
	}
}

func TestParseInputWithoutHeadersHasNoCategories(t *testing.T) {
	if r := ParseInput("a.com\nb.com"); r.Categories != nil {
		t.Fatalf("categories = %v", r.Categories)
	}
}

func TestParseInputBadCategoryHeader(t *testing.T) {
	r := ParseInput("// #cat: Без категории\na.com")
	if len(r.Invalid) != 1 || r.Invalid[0] != "// #cat: Без категории" {
		t.Fatalf("invalid = %v", r.Invalid)
	}
}

func TestParseInputMixedWithCategories(t *testing.T) {
	r := ParseInput("// #cat: x\na.com\n1.1.1.1")
	if !r.Mixed || r.Categories != nil {
		t.Fatalf("unexpected result %+v", r)
	}
}
