package checkers

import (
	"reflect"
	"testing"
)

func TestSql(t *testing.T) {
	xs := []string{"a", "b", "c", "d", "e", "f"}
	ys := chunks(xs, 1)
	zs := [][]string{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}, {"f"}}
	if !reflect.DeepEqual(ys, zs) {
		t.Errorf("%v is not equal to %v", ys, zs)
	}
	ys = chunks(xs, 2)
	zs = [][]string{{"a", "b"}, {"c", "d"}, {"e", "f"}}
	if !reflect.DeepEqual(ys, zs) {
		t.Errorf("%v is not equal to %v", ys, zs)
	}
	ys = chunks(xs, 4)
	zs = [][]string{{"a", "b", "c", "d"}, {"e", "f"}}
	if !reflect.DeepEqual(ys, zs) {
		t.Errorf("%v is not equal to %v", ys, zs)
	}
	ys = chunks(xs, 5)
	zs = [][]string{{"a", "b", "c", "d", "e"}, {"f"}}
	if !reflect.DeepEqual(ys, zs) {
		t.Errorf("%v is not equal to %v", ys, zs)
	}
	ys = chunks(xs, 6)
	zs = [][]string{{"a", "b", "c", "d", "e", "f"}}
	if !reflect.DeepEqual(ys, zs) {
		t.Errorf("%v is not equal to %v", ys, zs)
	}
	ys = chunks(xs, 7)
	zs = [][]string{{"a", "b", "c", "d", "e", "f"}}
	if !reflect.DeepEqual(ys, zs) {
		t.Errorf("%v is not equal to %v", ys, zs)
	}
}
