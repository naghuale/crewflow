package doctor

import "testing"

func TestFirstVersion(t *testing.T) {
	cases := []struct {
		output string
		want   string
	}{
		{"go version go1.27.1 darwin/arm64\n", "1.27.1"},
		{"golangci-lint has version 2.14.0 built with go1.27.1\n", "2.14.0"},
		{"libtdjson 1.2.3\n4.5.6\n", "1.2.3"},
		{"v0.42.1\n", "0.42.1"},
		{"built from source\n", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := firstVersion([]byte(tc.output)); got != tc.want {
			t.Errorf("firstVersion(%q) = %q, want %q", tc.output, got, tc.want)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		found   string
		min     string
		want    bool
		wantErr bool
	}{
		{found: "1.27.1", min: "1.27", want: true},
		{found: "1.26.3", min: "1.27", want: false},
		{found: "1.27", min: "1.27", want: true},
		{found: "1.27", min: "1.27.1", want: false},
		{found: "1.27.0", min: "1.27", want: true},
		// Numbers, not text: "10" is newer than "9" although it is smaller as
		// a string.
		{found: "10.0", min: "9.1", want: true},
		{found: "9.1", min: "10.0", want: false},
		{found: "2.0", min: "1.99.99", want: true},
		{found: "1.27", min: "nightly", wantErr: true},
		{found: "1.27", min: "", wantErr: true},
	}
	for _, tc := range cases {
		got, err := versionAtLeast(tc.found, tc.min)
		switch {
		case tc.wantErr && err == nil:
			t.Errorf("versionAtLeast(%q, %q) returned no error, want one", tc.found, tc.min)
		case !tc.wantErr && err != nil:
			t.Errorf("versionAtLeast(%q, %q) returned an error: %v", tc.found, tc.min, err)
		case !tc.wantErr && got != tc.want:
			t.Errorf("versionAtLeast(%q, %q) = %t, want %t", tc.found, tc.min, got, tc.want)
		}
	}
}
