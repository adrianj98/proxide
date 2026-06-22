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
	// streams framed output back over the same stream.
	StreamExec StreamType = 2
)

// ExecFrame tags each chunk the agent sends back on an exec stream.
type ExecFrame byte

const (
	// ExecOutput payload is raw combined stdout/stderr bytes.
	ExecOutput ExecFrame = 1
	// ExecResult payload is the final result: int32 exit code + cwd string.
	ExecResult ExecFrame = 2
)

const (
	maxCmdLen   = 1 << 20 // 1 MiB
	maxFrameLen = 1 << 20 // 1 MiB
)

// --- length-prefixed string helpers ---

func writeLenString(w io.Writer, s string) error {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(s)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := io.WriteString(w, s)
	return err
}

func readLenString(r io.Reader, max uint32) (string, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return "", err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > max {
		return "", fmt.Errorf("string too long: %d bytes", n)
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// --- stream type header ---

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

// --- exec request (edge -> agent) ---

// WriteExecRequest writes the starting working directory and command (after a
// StreamExec header). An empty cwd means "use the agent's default directory".
func WriteExecRequest(w io.Writer, cwd, cmd string) error {
	if len(cmd) > maxCmdLen {
		return fmt.Errorf("command too long: %d bytes", len(cmd))
	}
	if err := writeLenString(w, cwd); err != nil {
		return err
	}
	return writeLenString(w, cmd)
}

// ReadExecRequest reads the cwd and command written by WriteExecRequest.
func ReadExecRequest(r io.Reader) (cwd, cmd string, err error) {
	if cwd, err = readLenString(r, maxCmdLen); err != nil {
		return "", "", err
	}
	if cmd, err = readLenString(r, maxCmdLen); err != nil {
		return "", "", err
	}
	return cwd, cmd, nil
}

// --- exec output frames (agent -> edge) ---

// WriteExecOutput writes one output frame carrying raw bytes.
func WriteExecOutput(w io.Writer, p []byte) error {
	hdr := [5]byte{byte(ExecOutput)}
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(p)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(p)
	return err
}

// WriteExecResult writes the terminal frame with the command's exit code and the
// directory it ended in.
func WriteExecResult(w io.Writer, exit int32, cwd string) error {
	payload := make([]byte, 4+len(cwd))
	binary.BigEndian.PutUint32(payload[:4], uint32(exit))
	copy(payload[4:], cwd)

	hdr := [5]byte{byte(ExecResult)}
	binary.BigEndian.PutUint32(hdr[1:], uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ReadExecFrame reads the next exec frame: its type and raw payload.
func ReadExecFrame(r io.Reader) (ExecFrame, []byte, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[1:])
	if n > maxFrameLen {
		return 0, nil, fmt.Errorf("frame too long: %d bytes", n)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, err
	}
	return ExecFrame(hdr[0]), payload, nil
}

// ParseExecResult decodes an ExecResult payload into exit code and cwd.
func ParseExecResult(payload []byte) (exit int32, cwd string) {
	if len(payload) < 4 {
		return 0, ""
	}
	exit = int32(binary.BigEndian.Uint32(payload[:4]))
	return exit, string(payload[4:])
}
