package assp

import (
	"bufio"
	"io"
	"sync"
)

// Conn wraps a duplex byte stream (typically a *tls.Conn) with framed,
// concurrency-safe access. Multiple goroutines — the control loop plus one per
// active stream channel — may call Write concurrently; each frame is written
// atomically under a mutex so frames never interleave on the wire. Reads are
// single-consumer: one goroutine owns ReadFrame and dispatches by channel.
type Conn struct {
	rwc io.ReadWriteCloser
	br  *bufio.Reader

	wmu sync.Mutex // serializes frame writes
}

// NewConn wraps rwc. The caller retains responsibility for the underlying
// connection's lifecycle beyond Close.
func NewConn(rwc io.ReadWriteCloser) *Conn {
	return &Conn{
		rwc: rwc,
		br:  bufio.NewReaderSize(rwc, 64<<10),
	}
}

// ReadFrame reads the next frame. It must be called from a single goroutine.
func (c *Conn) ReadFrame() (Frame, error) {
	return ReadFrame(c.br)
}

// Write sends one frame atomically. Safe for concurrent use.
func (c *Conn) Write(ch uint16, typ, flags uint8, payload []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return WriteFrame(c.rwc, ch, typ, flags, payload)
}

// WriteFrame is a convenience wrapper taking a Frame value.
func (c *Conn) WriteFrame(f Frame) error {
	return c.Write(f.Channel, f.Type, f.Flags, f.Payload)
}

// Close closes the underlying connection.
func (c *Conn) Close() error { return c.rwc.Close() }
