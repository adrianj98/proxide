package tunnel

import (
	"io"
	"net"
	"sync"
)

// Pipe copies bytes bidirectionally between a and b until either side closes or
// errors, then closes both. This is correct for plain request/response,
// long-lived SSE streams (open until the client leaves), and WebSocket
// connections (open until either peer closes).
//
// Pipe blocks until both copy directions finish.
func Pipe(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	cp := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		// Unblock the other direction by closing both ends. Errors are ignored;
		// the connections are being torn down regardless.
		_ = a.Close()
		_ = b.Close()
	}

	go cp(a, b)
	go cp(b, a)
	wg.Wait()
}
