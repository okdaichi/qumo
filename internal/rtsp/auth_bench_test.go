package rtsp

import "testing"

// BenchmarkSelectQop measures the cost of parsing the RTSP Digest "qop"
// list during connection setup. The allocation-free IndexByte scan should
// report 0 allocs/op versus strings.Split's 1 alloc/op baseline.
func BenchmarkSelectQop(b *testing.B) {
	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"single_auth", "auth"},
		{"auth_first", "auth,auth-int"},
		{"auth_last", "auth-int,auth"},
		{"auth_only_int", "auth-int"},
		{"spaced", "auth, auth-int"},
		{"many", "auth,auth-int,auth,auth-int,auth"},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if got := selectQop(c.in); got != "" && got != "auth" {
					b.Fatalf("selectQop(%q) = %q", c.in, got)
				}
			}
		})
	}
}
