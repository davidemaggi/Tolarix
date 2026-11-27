package connectors

import (
	"reflect"
	"testing"
)

func TestCollapse(t *testing.T) {
	in := []Warning{
		{PageID: "1", Code: "a", Message: "x"},
		{PageID: "1", Code: "b", Message: "y"},
		{PageID: "1", Code: "a", Message: "x"},
		{PageID: "2", Code: "a", Message: "x"},
		{PageID: "1", Code: "a", Message: "x"},
	}
	got := Collapse(in)
	want := []Warning{
		{PageID: "1", Code: "a", Message: "x", Count: 3},
		{PageID: "1", Code: "b", Message: "y"},
		{PageID: "2", Code: "a", Message: "x"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Collapse = %+v, want %+v", got, want)
	}
}
