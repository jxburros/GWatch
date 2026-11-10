package selfconn

import (
	"context"
	"net"
	"testing"
)

func TestTrackRemembersLoopbackUntilClose(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()

	dial := DialContext((&net.Dialer{}).DialContext)
	conn, err := dial(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	defer server.Close()

	// The server sees the client's local address as its peer.
	peer := server.RemoteAddr().String()
	if !Is(peer) {
		t.Fatalf("an open self-dialled loopback connection should be known: %s", peer)
	}
	conn.Close()
	conn.Close() // a second Close must not unbalance the count
	if Is(peer) {
		t.Fatalf("a closed connection should be forgotten: %s", peer)
	}
}

func TestIsIgnoresUnknownAndMalformed(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1", "127.0.0.1:1", "[::1]:9", "not an address"} {
		if Is(addr) {
			t.Errorf("Is(%q) should be false", addr)
		}
	}
}
