package pawscript

import "testing"

// GetItems reaches a parsed document's list members, which come back as nodes.
func TestPSLGetItemsParsed(t *testing.T) {
	input := `(recent: ("a.go", "b.md", "c.txt"), tabSize: 4, showLineNumbers: true)`
	doc, err := ParsePSL(input)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
	}
	m := doc.Map()

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

	doc, err := ParsePSL(out)
	if err != nil {
		t.Fatalf("ParsePSL(%q): %v", out, err)
	}
	back := doc.Map()

	// Nested plain map must come back as a real nested structure.
	win := back.GetMap("window")
	if win == nil {
		t.Fatalf("window round-tripped as %T (%#v); want a nested list. serialized: %s",
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

// A PSL list holds an ordered sequence and a keyed map side by side, and one
// parse answers with both of them.
func TestAParseKeepsOrderedChildrenBesideNamedOnes(t *testing.T) {
	const input = `(_hash: "deadbeef", ("first"), ("second"), key: ("keyed"))`

	n, err := ParsePSL(input)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
	}
	if len(n.Named) != 2 {
		t.Fatalf("the node has %d keyed members, want 2: %#v", len(n.Named), n.Named)
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
	n, err := ParsePSL(`(a: "apple", b: "banana", "cherry", "dogfruit")`)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
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
	n, err := ParsePSL(`("first-item", "0": "keyed-zero")`)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
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
	n, err := ParsePSL(`(("ordered_record1", a: "one", b: 12))`)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
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
	n, err := ParsePSL(`(outer: (inner: ("deep-item"), "inner-item"), "outer-item")`)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
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

	first, err := ParsePSL(input)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
	}
	out := SerializePSLNode(first)

	second, err := ParsePSL(out)
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
	n, err := ParsePSL(`(tabSize: 4, name: "figaro", ("an-item"))`)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
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

	back, err := ParsePSL(SerializePSLNode(root))
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
		n, err := ParsePSL(in)
		if err != nil {
			t.Errorf("ParsePSL(%q): %v", in, err)
			continue
		}
		if n.Len() != 0 || len(n.Named) != 0 {
			t.Errorf("ParsePSL(%q) found %d items and %d members", in, n.Len(), len(n.Named))
		}
	}
	if _, err := ParsePSL(`no parentheses`); err == nil {
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

	n, ok := convertFromPawValue(list).(*PSLNode)
	if !ok {
		t.Fatalf("a StoredList converted to %T", convertFromPawValue(list))
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

// A nested list carries both collections too, so a document loses nothing at
// any depth rather than only at the top.
func TestANestedListKeepsBothCollections(t *testing.T) {
	n, err := ParsePSL(`(section: (a: "apple", "an-item", b: 2))`)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
	}
	sub, ok := n.Named["section"].(*PSLNode)
	if !ok {
		t.Fatalf("the nested list came back as %T, which can hold only one of its collections", n.Named["section"])
	}
	if v, _ := sub.Item(0); v != "an-item" {
		t.Errorf("the nested list's ordered child is %#v", v)
	}
	if v, _ := sub.Get("a"); v != "apple" {
		t.Errorf("the nested list's a is %#v", v)
	}
	if v, _ := sub.Get("b"); v != int64(2) {
		t.Errorf("the nested list's b is %#v", v)
	}
}

// A document with nothing but ordered children keeps them as children, not as
// keys that look like indices.
func TestOrderedChildrenAreNotFiledUnderNumbers(t *testing.T) {
	n, err := ParsePSL(`("first", "second")`)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
	}
	if n.Len() != 2 {
		t.Fatalf("the node has %d ordered children, want 2: %#v", n.Len(), n.Items)
	}
	if len(n.Named) != 0 {
		t.Errorf("the ordered children were filed as keyed members too: %#v", n.Named)
	}
}

// GetMap reads a section whichever shape it is in, so parsed and hand-built
// data behave the same.
func TestGetMapReadsAParsedSectionAndABuiltOne(t *testing.T) {
	n, err := ParsePSL(`(window: (width: 120, height: 40))`)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
	}
	if got := n.Map().GetMap("window").GetInt("width", -1); got != 120 {
		t.Errorf("a parsed section read back width %d", got)
	}

	m := PSLMap{
		"built":  PSLMap{"width": int64(7)},
		"plain":  map[string]interface{}{"width": int64(9)},
		"scalar": "hello",
	}
	if got := m.GetMap("built").GetInt("width", -1); got != 7 {
		t.Errorf("a PSLMap section read back width %d", got)
	}
	if got := m.GetMap("plain").GetInt("width", -1); got != 9 {
		t.Errorf("a plain map section read back width %d", got)
	}
	if got := m.GetMap("scalar"); got != nil {
		t.Errorf("a scalar answered GetMap with %#v", got)
	}
	if got := m.GetMap("absent"); got != nil {
		t.Errorf("a missing member answered GetMap with %#v", got)
	}
}

// GetItems reads a parsed list, which is a node.
func TestGetItemsReadsAParsedList(t *testing.T) {
	n, err := ParsePSL(`(recent: ("a.go", "b.md"))`)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
	}
	if got := n.Map().GetItems("recent"); len(got) != 2 {
		t.Fatalf("GetItems read %d items from a parsed list: %#v", len(got), got)
	}
}

// A parsed list can be put back inside a document being built by hand, which is
// what a caller does when it reads something, keeps part of it, and writes a
// new document around it. It has to write as the list it is.
func TestAParsedListSerializesInsideABuiltDocument(t *testing.T) {
	block, err := ParsePSL(`(a: "apple", "an-item")`)
	if err != nil {
		t.Fatalf("ParsePSL: %v", err)
	}

	for _, c := range []struct{ what, text string }{
		{"in a map", SerializePSL(PSLMap{"block": block})},
		{"in a list", SerializePSLList(PSLList{block})},
	} {
		doc, err := ParsePSL(c.text)
		if err != nil {
			t.Errorf("%s: re-parsing %q: %v", c.what, c.text, err)
			continue
		}
		var back *PSLNode
		if v, ok := doc.Get("block"); ok {
			back, _ = v.(*PSLNode)
		} else {
			back, _ = doc.Child(0)
		}
		if back == nil {
			t.Errorf("%s: the block came back as something other than a list: %q", c.what, c.text)
			continue
		}
		if v, _ := back.Get("a"); v != "apple" {
			t.Errorf("%s: the block's a came back as %#v: %q", c.what, v, c.text)
		}
		if v, _ := back.Item(0); v != "an-item" {
			t.Errorf("%s: the block's ordered child came back as %#v: %q", c.what, v, c.text)
		}
	}
}

// An empty list is what "()" means, so it is what "()" reads back as -- a list
// written empty and read again is still a list.
func TestAnEmptyListStaysAList(t *testing.T) {
	text := SerializePSL(PSLMap{"recent": PSLList{}, "window": PSLMap{}})
	doc, err := ParsePSL(text)
	if err != nil {
		t.Fatalf("ParsePSL(%q): %v", text, err)
	}
	for _, key := range []string{"recent", "window"} {
		v, ok := doc.Get(key)
		if !ok {
			t.Errorf("%s went missing: %q", key, text)
			continue
		}
		n, ok := v.(*PSLNode)
		if !ok {
			t.Errorf("%s came back as %T (%#v): %q", key, v, v, text)
			continue
		}
		if n.Len() != 0 || len(n.Named) != 0 {
			t.Errorf("%s came back holding %d items and %d members", key, n.Len(), len(n.Named))
		}
	}
}

// A bare word and a quoted string are different values, and a parse keeps them
// apart: `kind: text` names the identifier text and `name: "text"` holds the
// four characters. Flattening both to a Go string threw that away on the way
// in while the serializer went on writing them back out differently.
func TestABareWordStaysABareWord(t *testing.T) {
	n, err := ParsePSL(`(kind: text, name: "text", tags: (red, "blue"))`)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := n.Get("kind"); v != Symbol("text") {
		t.Errorf("a bare word came through as %T %#v", v, v)
	}
	if v, _ := n.Get("name"); v != "text" {
		t.Errorf("a quoted string came through as %T %#v", v, v)
	}
	tags, ok := nodeUnder(n, "tags")
	if !ok {
		t.Fatal("the nested list did not come through")
	}
	if v, _ := tags.Item(0); v != Symbol("red") {
		t.Errorf("a bare word inside a list came through as %T %#v", v, v)
	}
	if v, _ := tags.Item(1); v != "blue" {
		t.Errorf("a quoted string inside a list came through as %T %#v", v, v)
	}
}

// nodeUnder is the node filed under a key, where there is one.
func nodeUnder(n *PSLNode, key string) (*PSLNode, bool) {
	v, ok := n.Get(key)
	if !ok {
		return nil, false
	}
	c, ok := v.(*PSLNode)
	return c, ok
}

// Three bare words are values rather than identifiers, and those still convert
// to what they say.
func TestTheThreeWordsThatAreValuesStillConvert(t *testing.T) {
	n, err := ParsePSL(`(on: true, off: false, missing: nil)`)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := n.Get("on"); v != true {
		t.Errorf("true came through as %T %#v", v, v)
	}
	if v, _ := n.Get("off"); v != false {
		t.Errorf("false came through as %T %#v", v, v)
	}
	if v, ok := n.Get("missing"); !ok || v != nil {
		t.Errorf("nil came through as %T %#v", v, v)
	}

	// Text never reaches the conversion carrying one of the three -- the
	// argument parser has turned them into Go values already -- but a
	// StoredList handed over by the executor can, and it is converted there.
	for _, c := range []struct {
		word Symbol
		want interface{}
	}{{"nil", nil}, {"true", true}, {"false", false}, {"other", Symbol("other")}} {
		if got := convertFromPawValue(c.word); got != c.want {
			t.Errorf("the word %s converted to %T %#v", c.word, got, got)
		}
	}
}

// Which is what makes a document survive a round trip: bare goes back out bare
// and quoted goes back out quoted, rather than everything coming back quoted.
func TestBareAndQuotedSurviveARoundTrip(t *testing.T) {
	const text = `(kind: text, name: "text")`
	n, err := ParsePSL(text)
	if err != nil {
		t.Fatal(err)
	}
	if got := SerializePSLNode(n); got != text {
		t.Errorf("the round trip came back as %s", got)
	}
}

// And it lets a document built by hand say a bare word at all, which nothing
// could before: every Go string was written quoted.
func TestANodeBuiltByHandCanSayABareWord(t *testing.T) {
	n := NewPSLNode()
	n.Named["mode"] = Symbol("auto")
	n.Named["title"] = "auto"
	if got := SerializePSLNode(n); got != `(mode: auto, title: "auto")` {
		t.Errorf("a hand-built document came out as %s", got)
	}
}

// The typed accessors read a bare word as the text it spells, so a config file
// written `theme: dark` reads the same way it always did.
func TestTheTypedAccessorsReadABareWord(t *testing.T) {
	n, err := ParsePSL(`(theme: dark, port: 8080, on: yes)`)
	if err != nil {
		t.Fatal(err)
	}
	m := n.Map()
	if got := m.GetString("theme", "?"); got != "dark" {
		t.Errorf("GetString read a bare word as %q", got)
	}
	if got := m.GetInt("port", 0); got != 8080 {
		t.Errorf("GetInt read %d", got)
	}
	if !m.GetBool("on", false) {
		t.Error("GetBool did not read the bare word yes")
	}

	// A bare word is never all digits -- text that is parses as a number -- so
	// a Symbol reaches the number accessors only from a document built by hand,
	// where a caller said one on purpose.
	built := PSLMap{"port": Symbol("8080"), "ratio": Symbol("1.5"), "on": Symbol("1")}
	if got := built.GetInt("port", 0); got != 8080 {
		t.Errorf("GetInt read a hand-set bare word as %d", got)
	}
	if got := built.GetFloat("ratio", 0); got != 1.5 {
		t.Errorf("GetFloat read a hand-set bare word as %v", got)
	}
	if !built.GetBool("on", false) {
		t.Error("GetBool did not read the hand-set bare word 1")
	}
}
