//go:build linux

package commander

import (
	"reflect"
	"testing"
)

func TestSplitIPFamilies(t *testing.T) {
	v4, v6 := splitIPFamilies([]string{"203.0.113.7", "2001:db8::1", "198.51.100.2", "fe80::1"})
	if !reflect.DeepEqual(v4, []string{"203.0.113.7", "198.51.100.2"}) || !reflect.DeepEqual(v6, []string{"2001:db8::1", "fe80::1"}) {
		t.Fatalf("v4=%v v6=%v", v4, v6)
	}
}
