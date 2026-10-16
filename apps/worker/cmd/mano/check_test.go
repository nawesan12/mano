package main

import "testing"

func TestPublicCandidate(t *testing.T) {
	priv := "a=candidate:1 1 udp 2130706431 192.168.0.10 50000 typ host\r\n"
	pub := "a=candidate:2 1 udp 1694498815 200.45.10.3 50001 typ srflx raddr 0.0.0.0 rport 0\r\n"
	if publicCandidate(priv) || !publicCandidate(priv+pub) || publicCandidate("a=candidate:3 1 tcp 1 200.45.10.3 9 typ host\r\n") {
		t.Fatal("publicCandidate")
	}
}
