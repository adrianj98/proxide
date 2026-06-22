package tunnel

import (
	"encoding/binary"
	"fmt"
	"io"
)

// StreamType is the first byte the edge writes on every yamux stream it opens,
// so the agent knows how to handle it.
type StreamType byte

const (
	// StreamProxy carries raw bytes piped to the agent's local target (the
	// default L4 tunnel behaviour).
	StreamProxy StreamType = 1
	// StreamExec carries a command to run inside the container; the agent
	// streams the combined stdout/stderr back over the same stream.
	StreamExec StreamType = 2
)

// maxCmdLen bounds the command size to avoid unbounded allocation.
const maxCmdLen = 1 << 20 // 1 MiB

// WriteStreamType writes the one-byte stream type header.
func WriteStreamType(w io.Writer, t StreamType) error {
	_, err := w.Write([]byte{byte(t)})
	return err
}

// ReadStreamType reads the one-byte stream type header.
func ReadStreamType(r io.Reader) (StreamType, error) {
	var b [1]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return StreamType(b[0]), nil
}

// WriteExecRequest writes a length-prefixed command (used after StreamExec).
func WriteExecRequest(w io.Writer, cmd string) error {
	if len(cmd) > maxCmdLen {
		return fmt.Errorf("command too long: %d bytes", len(cmd))
	}
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(cmd)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := io.WriteString(w, cmd)
	return err
}

// ReadExecRequest reads a length-prefixed command written by WriteExecRequest.
func ReadExecRequest(r io.Reader) (string, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxCmdLen {
		return "", fmt.Errorf("command too long: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}
