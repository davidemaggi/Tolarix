package model

import "testing"

func TestTreeOrderingAndAncestors(t *testing.T) {
	tr := NewTree()
	tr.Add(&Page{ID: "b", Title: "B", Order: 2})
	tr.Add(&Page{ID: "a", Title: "A", Order: 1})
	tr.Add(&Page{ID: "a/y", ParentID: "a", Title: "Y"})
	tr.Add(&Page{ID: "a/x", ParentID: "a", Title: "X"})
	tr.Add(&Page{ID: "a/x/deep", ParentID: "a/x", Title: "Deep"})

	var got []string
	tr.Walk(func(p *Page, depth int) { got = append(got, p.ID) })
	want := []string{"a", "a/x", "a/x/deep", "a/y", "b"}
	if len(got) != len(want) {
		t.Fatalf("walk %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("walk %v want %v", got, want)
		}
	}
	anc := tr.Ancestors(tr.Pages["a/x/deep"])
	if len(anc) != 2 || anc[0].ID != "a" || anc[1].ID != "a/x" {
		t.Fatalf("ancestors %v", anc)
	}
}

func TestAncestorsSurvivesCycles(t *testing.T) {
	tr := NewTree()
	tr.Add(&Page{ID: "a", ParentID: "b"})
	tr.Add(&Page{ID: "b", ParentID: "a"})
	if n := len(tr.Ancestors(tr.Pages["a"])); n != 1 {
		t.Fatalf("got %d", n)
	}
}

func TestLinks(t *testing.T) {
	u := PageLink("Folder/My Page", "Section 2")
	if u != "tolarix://page/Folder%2FMy%20Page#Section%202" {
		t.Fatalf("link %q", u)
	}
	id, h, ok := ParsePageLink(u)
	if !ok || id != "Folder/My Page" || h != "Section 2" {
		t.Fatalf("parse %q %q %v", id, h, ok)
	}
	if _, _, ok := ParsePageLink("https://x"); ok {
		t.Fatal("non-canonical accepted")
	}
	if h, ok := ParseAttachmentLink(AttachmentLink("abc")); !ok || h != "abc" {
		t.Fatal("att link")
	}
}

func TestContentHashIgnoresPosition(t *testing.T) {
	a := &Page{ID: "x", Title: "T", Body: "b", Order: 1}
	b := &Page{ID: "y", ParentID: "p", Title: "T", Body: "b", Order: 9}
	if a.ContentHash() != b.ContentHash() {
		t.Fatal("position must not affect content hash")
	}
	b.Body = "c"
	if a.ContentHash() == b.ContentHash() || a.BodyHash() == b.BodyHash() {
		t.Fatal("body must affect hashes")
	}
}
