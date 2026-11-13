package main

import "testing"

func TestUTF8CompleteLen(t *testing.T) {
	zh := []byte("中文") // 6 bytes
	cases := []struct {
		in   []byte
		want int
	}{
		{[]byte("abc"), 3},
		{zh, 6},
		{zh[:5], 3},
		{zh[:4], 3},
		{append([]byte("a"), zh[:1]...), 1},
		{[]byte{}, 0},
		{[]byte("😀")[:3], 0},
		{[]byte("😀"), 4},
	}
	for _, c := range cases {
		if got := utf8CompleteLen(c.in); got != c.want {
			t.Errorf("utf8CompleteLen(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}
