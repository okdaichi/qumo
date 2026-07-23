package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseSessions(t *testing.T) {
	tests := map[string]struct {
		in      string
		want    []int
		wantErr bool
	}{
		"spaces":            {in: "2000 5000 8000", want: []int{2000, 5000, 8000}},
		"commas":            {in: "500,1000,2000", want: []int{500, 1000, 2000}},
		"mixed + extra ws":  {in: " 500,  1000\t2000 ", want: []int{500, 1000, 2000}},
		"single":            {in: "12000", want: []int{12000}},
		"empty":             {in: "   ", wantErr: true},
		"non-numeric":       {in: "500 abc", wantErr: true},
		"zero not positive": {in: "0", wantErr: true},
		"negative":          {in: "-5", wantErr: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := parseSessions(tt.in)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseLastRecord(t *testing.T) {
	t.Run("returns the last record", func(t *testing.T) {
		body := []byte(`{"sessions":2000,"verdict":"HOLDS","receiving":2000}
{"sessions":5000,"verdict":"CANNOT-HOLD","receiving":4100}
`)
		rec, err := parseLastRecord(body)
		require.NoError(t, err)
		assert.Equal(t, 5000, rec.Sessions)
		assert.Equal(t, "CANNOT-HOLD", rec.Verdict)
		assert.Equal(t, 4100, rec.Receiving)
	})
	t.Run("ignores blank lines", func(t *testing.T) {
		body := []byte("\n{\"sessions\":100,\"verdict\":\"HOLDS\"}\n\n")
		rec, err := parseLastRecord(body)
		require.NoError(t, err)
		assert.Equal(t, "HOLDS", rec.Verdict)
	})
	t.Run("empty is an error", func(t *testing.T) {
		_, err := parseLastRecord([]byte("\n  \n"))
		assert.Error(t, err)
	})
	t.Run("malformed json is an error", func(t *testing.T) {
		_, err := parseLastRecord([]byte(`{"sessions":1} then garbage`))
		assert.Error(t, err)
	})
}

func TestRun_ModeValidation(t *testing.T) {
	t.Run("neither --sessions nor --auto errors", func(t *testing.T) {
		assert.Error(t, run([]string{"--start-relay"}))
	})
	t.Run("both --sessions and --auto errors", func(t *testing.T) {
		assert.Error(t, run([]string{"--start-relay", "--auto", "--sessions", "1000"}))
	})
	t.Run("--sessions with a bad list errors", func(t *testing.T) {
		assert.Error(t, run([]string{"--start-relay", "--sessions", "abc"}))
	})
	t.Run("--auto with bad search params errors", func(t *testing.T) {
		assert.Error(t, run([]string{"--start-relay", "--auto", "--start", "0"}))
	})
}
