package adt

import "testing"

// encodeNamespacePath computed idx := strings.Index(path, "//") over the
// FULL string (path + query) before splitting the query off, then sliced
// path[:idx+1] against the QUERY-STRIPPED path. If a "//" only occurs inside
// the query (a base64-shaped lock handle can easily contain one — see
// aibap.mcp#494 / adtler#131) and the stripped path is shorter than idx+1,
// this panics: "slice bounds out of range". No recover() sits above
// doMutateWith, so this crashes the whole process. Found via adversarial
// review of #131, which only incidentally avoided it by percent-encoding
// the query before it ever reaches this function — the root cause here
// remained live for any other caller.
func TestEncodeNamespacePath_QuerySlashesDoNotPanic(t *testing.T) {
	// No genuine namespace in the path; the only "//" is inside the query.
	// Must return the input unchanged, not panic.
	in := "/sap/bc/adt/programs/programs/ZTEST?_action=UNLOCK&lockHandle=AB//CD"
	got := encodeNamespacePath(in)
	if got != in {
		t.Errorf("got %q, want unchanged %q", got, in)
	}
}

func TestEncodeNamespacePath_NamespaceInPathStillEncoded(t *testing.T) {
	// Regression: a genuine customer-namespace class path (the classrun.go
	// case: appending "/na2/foo" after a base with no trailing slash
	// produces "//na2/foo") must still be percent-encoded correctly.
	in := "/sap/bc/adt/oo/classrun//na2/foo"
	want := "/sap/bc/adt/oo/classrun/%2fna2%2ffoo"
	got := encodeNamespacePath(in)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEncodeNamespacePath_NamespaceInPathPlusQuerySlashes(t *testing.T) {
	// Both a real namespace segment AND an unrelated "//" in the query must
	// work together: namespace gets encoded, query passes through as-is.
	in := "/sap/bc/adt/oo/classrun//na2/foo?_action=UNLOCK&lockHandle=AB//CD"
	want := "/sap/bc/adt/oo/classrun/%2fna2%2ffoo?_action=UNLOCK&lockHandle=AB//CD"
	got := encodeNamespacePath(in)
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestEncodeNamespacePath_NoSlashesAtAll(t *testing.T) {
	in := "/sap/bc/adt/programs/programs/ZTEST"
	got := encodeNamespacePath(in)
	if got != in {
		t.Errorf("got %q, want unchanged %q", got, in)
	}
}
