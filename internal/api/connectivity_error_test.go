package api_test

import (
	"testing"

	"github.com/foisalislambd/openbackup/internal/api"
)

func TestIsConnectivityError(t *testing.T) {
	t.Parallel()
	cases := []struct {
		msg  string
		want bool
	}{
		{"", false},
		{"Post \"https://test.foisal.org/api/v1/agent/snapshots\": dial tcp: lookup test.foisal.org: no such host", true},
		{"dial tcp 1.2.3.4:443: connect: connection refused", true},
		{"Get \"https://x\": context deadline exceeded (Client.Timeout exceeded while awaiting headers)", true},
		{"i/o timeout", true},
		{"the server is out of space for this account", false},
		{"chunk checksum mismatch", false},
		{"connection reset by peer", true},
		{"tls: failed to verify certificate", true},
		{"Backup failed: dial tcp: lookup test.foisal.org: no such host", true},
		{"missing encryption certificate material", false},
	}
	for _, tc := range cases {
		if got := api.IsConnectivityError(tc.msg); got != tc.want {
			t.Errorf("IsConnectivityError(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}
