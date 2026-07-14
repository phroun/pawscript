package pawscript

import "testing"

// TestPSLGetItemsParsed guards the GetItems accessor against a regression where
// it only matched the unnamed []interface{} type. The parser (convertFromPawValue)
// always produces nested lists as the named PSLList type, so GetItems must accept
// PSLList too — otherwise every parsed document returns nil for its list fields.
func TestPSLGetItemsParsed(t *testing.T) {
	input := `(recent: ("a.go", "b.md", "c.txt"), tabSize: 4, showLineNumbers: true)`
	m, err := ParsePSL(input)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
	}

	items := m.GetItems("recent")
	if len(items) != 3 {
		t.Fatalf("GetItems(recent) = %#v (len %d); want 3 items from parsed PSLList",
			items, len(items))
	}
	want := []string{"a.go", "b.md", "c.txt"}
	for i, w := range want {
		if got, _ := items[i].(string); got != w {
			t.Errorf("item[%d] = %#v; want %q", i, items[i], w)
		}
	}

	// Scalars must survive the round-trip through the same map.
	if m.GetInt("tabSize", -1) != 4 {
		t.Errorf("GetInt(tabSize) = %d; want 4", m.GetInt("tabSize", -1))
	}
	if !m.GetBool("showLineNumbers", false) {
		t.Errorf("GetBool(showLineNumbers) = false; want true")
	}
}

// TestPSLGetItemsSetPSLList mirrors the pawgui-qt usage pattern: a caller stores a
// PSLList in-memory via Set and reads it back with GetItems in the same process
// (no serialization round-trip). This is exactly what launcher_position/size do.
func TestPSLGetItemsSetPSLList(t *testing.T) {
	m := PSLMap{}
	m.Set("launcher_position", PSLList{int64(120), int64(40)})

	items := m.GetItems("launcher_position")
	if len(items) != 2 {
		t.Fatalf("GetItems(launcher_position) = %#v (len %d); want 2", items, len(items))
	}

	// A plain []interface{} stored by a caller must still work (backward compat).
	m.Set("plain", []interface{}{1, 2, 3})
	if got := m.GetItems("plain"); len(got) != 3 {
		t.Fatalf("GetItems(plain) = %#v (len %d); want 3", got, len(got))
	}

	// A non-list value returns nil, not a panic.
	m.Set("scalar", "hello")
	if got := m.GetItems("scalar"); got != nil {
		t.Errorf("GetItems(scalar) = %#v; want nil", got)
	}
}
