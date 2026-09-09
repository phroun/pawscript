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

// TestPSLSerializePlainMapAndSlice checks that caller-built plain map[string]interface{}
// and []interface{} values serialize into real nested PSL structures rather than the
// fmt.Sprintf "map[...]" string the default branch used to emit. A parse round-trip
// must recover the structure, not a stringified blob.
func TestPSLSerializePlainMapAndSlice(t *testing.T) {
	src := PSLMap{
		"window": map[string]interface{}{"width": int64(120), "height": int64(40)},
		"recent": []interface{}{"a.go", "b.md"},
	}
	out := SerializePSL(src)

	back, err := ParsePSL(out)
	if err != nil {
		t.Fatalf("ParsePSL(%q): %v", out, err)
	}

	// Nested plain map must come back as a real nested structure.
	win, ok := back["window"].(PSLMap)
	if !ok {
		t.Fatalf("window round-tripped as %T (%#v); want a nested PSLMap. serialized: %s",
			back["window"], back["window"], out)
	}
	if win.GetInt("width", -1) != 120 || win.GetInt("height", -1) != 40 {
		t.Errorf("window = %#v; want width 120, height 40", win)
	}

	// Nested plain slice must come back as list items, not a string.
	items := back.GetItems("recent")
	if len(items) != 2 {
		t.Fatalf("recent = %#v (len %d); want 2 items. serialized: %s",
			back["recent"], len(items), out)
	}
	if s, _ := items[0].(string); s != "a.go" {
		t.Errorf("recent[0] = %#v; want %q", items[0], "a.go")
	}
}

// A PSL list holds an ordered sequence and a keyed map side by side. PSLMap can
// only hold the second, so ParsePSL keeps positional items only when there are
// no named members at all. PSLNode holds both, which is what the format has
// always carried.
func TestPSLNodeKeepsOrderedChildrenBesideNamedOnes(t *testing.T) {
	const input = `(_hash: "deadbeef", ("first"), ("second"), key: ("keyed"))`

	// The map wrapper drops the ordered children, which is why the node exists.
	m, err := ParsePSL(input)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
	}
	if len(m) != 2 {
		t.Fatalf("ParsePSL kept %d members: %#v", len(m), m)
	}

	n, err := ParsePSLNode(input)
	if err != nil {
		t.Fatalf("ParsePSLNode: %v", err)
	}
	if n.Len() != 2 {
		t.Fatalf("the node has %d ordered children, want 2: %#v", n.Len(), n.Items)
	}
	if v, ok := n.Get("_hash"); !ok || v != "deadbeef" {
		t.Errorf("_hash = %#v (present %v)", v, ok)
	}
	if _, ok := n.Get("key"); !ok {
		t.Error("the keyed record went missing")
	}
}

// Keyed members occupy no positions, so a child's index is decided by the
// children alone: adding metadata cannot shift a record.
func TestKeyedMembersConsumeNoIndices(t *testing.T) {
	n, err := ParsePSLNode(`(a: "apple", b: "banana", "cherry", "dogfruit")`)
	if err != nil {
		t.Fatalf("ParsePSLNode: %v", err)
	}
	if n.Len() != 2 {
		t.Fatalf("the node has %d ordered children, want 2", n.Len())
	}
	if v, _ := n.Item(0); v != "cherry" {
		t.Errorf("item 0 is %#v, want the first POSITIONAL item", v)
	}
	if v, _ := n.Item(1); v != "dogfruit" {
		t.Errorf("item 1 is %#v", v)
	}
	if v, _ := n.Get("a"); v != "apple" {
		t.Errorf("the keyed member a is %#v", v)
	}
	if _, ok := n.Item(2); ok {
		t.Error("a third child was found where there are only two")
	}
}

// A key that looks like a number is still a key, in the other collection.
func TestANumericKeyDoesNotShadowAnIndex(t *testing.T) {
	n, err := ParsePSLNode(`("first-item", "0": "keyed-zero")`)
	if err != nil {
		t.Fatalf("ParsePSLNode: %v", err)
	}
	if v, _ := n.Item(0); v != "first-item" {
		t.Errorf("index 0 gave %#v, not the ordered child", v)
	}
	if v, _ := n.Get("0"); v != "keyed-zero" {
		t.Errorf("the key \"0\" gave %#v", v)
	}
}

// A record can hold a leading item and named members at once, and both survive
// -- which is the shape a bundle's records are written in.
func TestARecordKeepsItsLeadingItemAndItsFields(t *testing.T) {
	n, err := ParsePSLNode(`(("ordered_record1", a: "one", b: 12))`)
	if err != nil {
		t.Fatalf("ParsePSLNode: %v", err)
	}
	rec, ok := n.Child(0)
	if !ok {
		t.Fatalf("the record did not come back as a list: %#v", n.Items)
	}
	if v, _ := rec.Item(0); v != "ordered_record1" {
		t.Errorf("the record's leading item is %#v", v)
	}
	if v, _ := rec.Get("a"); v != "one" {
		t.Errorf("the record's a is %#v", v)
	}
	if v, _ := rec.Get("b"); v != int64(12) {
		t.Errorf("the record's b is %#v", v)
	}
}

// Nesting survives to any depth, in both collections.
func TestNestingSurvivesInBothCollections(t *testing.T) {
	n, err := ParsePSLNode(`(outer: (inner: ("deep-item"), "inner-item"), "outer-item")`)
	if err != nil {
		t.Fatalf("ParsePSLNode: %v", err)
	}
	if v, _ := n.Item(0); v != "outer-item" {
		t.Errorf("the outer item is %#v", v)
	}
	outer, ok := n.Named["outer"].(*PSLNode)
	if !ok {
		t.Fatalf("outer is %T, not a node", n.Named["outer"])
	}
	if v, _ := outer.Item(0); v != "inner-item" {
		t.Errorf("outer's ordered child is %#v", v)
	}
	inner, ok := outer.Named["inner"].(*PSLNode)
	if !ok {
		t.Fatalf("inner is %T, not a node", outer.Named["inner"])
	}
	if v, _ := inner.Item(0); v != "deep-item" {
		t.Errorf("inner's ordered child is %#v", v)
	}
}

// What is written out comes back the same, ordered children included.
func TestANodeSurvivesARoundTrip(t *testing.T) {
	const input = `(_bundle: (key: "figaro"), _hash: "deadbeef", ` +
		`("ordered_record1", a: "one"), ("ordered_record2", a: "two"), ` +
		`someKey: ("keyed_record", a: "three"))`

	first, err := ParsePSLNode(input)
	if err != nil {
		t.Fatalf("ParsePSLNode: %v", err)
	}
	out := SerializePSLNode(first)

	second, err := ParsePSLNode(out)
	if err != nil {
		t.Fatalf("re-parsing %q: %v", out, err)
	}
	if second.Len() != first.Len() {
		t.Fatalf("%d children went out and %d came back: %q", first.Len(), second.Len(), out)
	}
	for i := range first.Items {
		a, _ := first.Child(i)
		b, _ := second.Child(i)
		if a == nil || b == nil {
			t.Fatalf("child %d is not a list on one side", i)
		}
		av, _ := a.Item(0)
		bv, _ := b.Item(0)
		if av != bv {
			t.Errorf("child %d led with %#v and came back as %#v", i, av, bv)
		}
	}
	if v, _ := second.Get("_hash"); v != "deadbeef" {
		t.Errorf("_hash came back as %#v: %q", v, out)
	}
	bundle, ok := second.Named["_bundle"].(*PSLNode)
	if !ok {
		t.Fatalf("_bundle came back as %T", second.Named["_bundle"])
	}
	if v, _ := bundle.Get("key"); v != "figaro" {
		t.Errorf("_bundle.key came back as %#v", v)
	}
}

// The keyed members are the map the typed accessors are written against, and
// the node shares its storage rather than copying it.
func TestTheNamedMembersAreAPSLMap(t *testing.T) {
	n, err := ParsePSLNode(`(tabSize: 4, name: "figaro", ("an-item"))`)
	if err != nil {
		t.Fatalf("ParsePSLNode: %v", err)
	}
	m := n.Map()
	if got := m.GetInt("tabSize", -1); got != 4 {
		t.Errorf("GetInt(tabSize) = %d", got)
	}
	if got := m.GetString("name", ""); got != "figaro" {
		t.Errorf("GetString(name) = %q", got)
	}
	m["added"] = "through the map"
	if v, ok := n.Get("added"); !ok || v != "through the map" {
		t.Error("the map is a copy rather than the node's own storage")
	}
}

// A node built by hand serializes, so the type is a way to write PSL as well
// as to read it.
func TestANodeBuiltByHandSerializes(t *testing.T) {
	rec := NewPSLNode()
	rec.Items = append(rec.Items, "a-record")
	rec.Named["n"] = 1

	root := NewPSLNode()
	root.Named["_hash"] = "deadbeef"
	root.Items = append(root.Items, rec)

	back, err := ParsePSLNode(SerializePSLNode(root))
	if err != nil {
		t.Fatalf("re-parsing what was written: %v", err)
	}
	if v, _ := back.Get("_hash"); v != "deadbeef" {
		t.Errorf("_hash came back as %#v", v)
	}
	child, ok := back.Child(0)
	if !ok {
		t.Fatalf("the record did not come back: %#v", back.Items)
	}
	if v, _ := child.Item(0); v != "a-record" {
		t.Errorf("the record led with %#v", v)
	}
	if v, _ := child.Get("n"); v != int64(1) {
		t.Errorf("the record's n came back as %#v", v)
	}
}

// Empty and malformed input are answered the way the map parser answers them.
func TestANodeAnswersEmptyAndMalformedTheSameWay(t *testing.T) {
	for _, in := range []string{"", "  ", "()", "# just a comment"} {
		n, err := ParsePSLNode(in)
		if err != nil {
			t.Errorf("ParsePSLNode(%q): %v", in, err)
			continue
		}
		if n.Len() != 0 || len(n.Named) != 0 {
			t.Errorf("ParsePSLNode(%q) found %d items and %d members", in, n.Len(), len(n.Named))
		}
	}
	if _, err := ParsePSLNode(`no parentheses`); err == nil {
		t.Error("unparenthesised input was accepted")
	}
	if got := SerializePSLNode(nil); got != "()" {
		t.Errorf("a nil node serializes to %q", got)
	}
	if got := SerializePSLNode(NewPSLNode()); got != "()" {
		t.Errorf("an empty node serializes to %q", got)
	}
}

// A value that is already a StoredList -- one handed over from the executor
// rather than read from text -- carries both collections too.
func TestAStoredListConvertsWithBothCollections(t *testing.T) {
	inner := NewStoredListWithNamed(
		[]interface{}{QuotedString("a-record")},
		map[string]interface{}{"n": int64(1)},
	)
	list := NewStoredListWithNamed(
		[]interface{}{inner},
		map[string]interface{}{"_hash": QuotedString("deadbeef")},
	)

	n, ok := pawValueToNode(list).(*PSLNode)
	if !ok {
		t.Fatalf("a StoredList converted to %T", pawValueToNode(list))
	}
	if v, _ := n.Get("_hash"); v != "deadbeef" {
		t.Errorf("_hash came through as %#v", v)
	}
	child, ok := n.Child(0)
	if !ok {
		t.Fatalf("the ordered child did not come through: %#v", n.Items)
	}
	if v, _ := child.Item(0); v != "a-record" {
		t.Errorf("the child led with %#v", v)
	}
	if v, _ := child.Get("n"); v != int64(1) {
		t.Errorf("the child's n came through as %#v", v)
	}
}
