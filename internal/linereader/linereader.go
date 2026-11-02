// Package linereader provides a newline-delimited line reader that tolerates
// lines of any size with bounded memory.
//
// bufio.Scanner stops permanently (ErrTooLong) when a single line exceeds its
// buffer. For long-lived readers such as the holder's CLI stdout reader that
// means every later line is silently lost. Reader instead reports an oversized
// line as such (keeping only a short prefix), drains the rest of it without
// buffering, and continues with the next line.
package linereader

import (
	"bufio"
	"io"
)

const (
	// DefaultMaxLine is the default maximum size (bytes, excluding the line
	// terminator) of a line that is returned in full. It matches the 10MiB cap
	// the bufio.Scanner based readers used before.
	DefaultMaxLine = 10 * 1024 * 1024
	// DefaultKeepPrefix is the default number of leading bytes kept for an
	// oversized line.
	DefaultKeepPrefix = 4096
	readBufSize       = 64 * 1024
	// retainBufCap is the largest accumulation buffer kept between calls.
	retainBufCap = 1024 * 1024
)

// Options configures a Reader. Zero values select the defaults.
type Options struct {
	// MaxLine is the largest line (excluding "\n" and a trailing "\r") that is
	// returned whole.
	MaxLine int
	// KeepPrefix is how many leading bytes of an oversized line are returned.
	KeepPrefix int
	// Observe, if set, is called for an oversized line with its content in
	// order, in chunks: first with the bytes accumulated so far (at most about
	// MaxLine bytes, already held in memory) and then with each following
	// chunk (at most one read buffer, ~64KiB). Chunks exclude the line
	// terminator and may include a trailing "\r". The slice is only valid
	// during the call. It lets callers sniff a few fields of a line that is
	// otherwise discarded.
	Observe func(chunk []byte)
}

// Line is one line read by Reader.Next.
type Line struct {
	// Data is the whole line without its terminator ("\n" or "\r\n"), or, if
	// Oversized, only its first KeepPrefix bytes. Valid until the next call.
	Data []byte
	// Size is the original size in bytes without the terminator.
	Size int64
	// Oversized is true when Size exceeded MaxLine and Data is only a prefix.
	Oversized bool
}

// Reader reads lines from an io.Reader.
type Reader struct {
	br  *bufio.Reader
	opt Options
	buf []byte
	err error
}

// New returns a Reader reading from r.
func New(r io.Reader, opt Options) *Reader {
	if opt.MaxLine <= 0 {
		opt.MaxLine = DefaultMaxLine
	}
	if opt.KeepPrefix <= 0 {
		opt.KeepPrefix = DefaultKeepPrefix
	}
	return &Reader{br: bufio.NewReaderSize(r, readBufSize), opt: opt}
}

// goOver switches to discard mode: reports the bytes held so far to Observe
// and shrinks the buffer to the kept prefix.
func (r *Reader) goOver() {
	if r.opt.Observe != nil {
		r.opt.Observe(r.buf)
	}
	if len(r.buf) > r.opt.KeepPrefix {
		r.buf = r.buf[:r.opt.KeepPrefix]
	}
}

// Next returns the next line. It returns io.EOF (or the underlying read error)
// once the input is exhausted; a final line without a terminator is returned
// first, with a nil error. Empty lines are returned as empty Data.
func (r *Reader) Next() (Line, error) {
	if r.err != nil {
		return Line{}, r.err
	}
	if cap(r.buf) > retainBufCap {
		r.buf = nil
	}
	r.buf = r.buf[:0]

	var size int64
	over := false
	var last byte
	for {
		chunk, err := r.br.ReadSlice('\n')
		complete := err == nil
		if err != nil && err != bufio.ErrBufferFull {
			r.err = err
		}
		data := chunk
		if complete {
			data = chunk[:len(chunk)-1]
		}
		size += int64(len(data))
		if len(data) > 0 {
			last = data[len(data)-1]
		}
		if !over {
			r.buf = append(r.buf, data...)
			// One extra byte of room for a trailing "\r" that is stripped later.
			if len(r.buf) > r.opt.MaxLine+1 {
				over = true
				r.goOver()
			}
		} else if r.opt.Observe != nil && len(data) > 0 {
			r.opt.Observe(data)
		}
		if complete {
			break
		}
		if err != bufio.ErrBufferFull {
			if size == 0 {
				return Line{}, r.err
			}
			break
		}
	}

	if !over {
		if n := len(r.buf); n > 0 && r.buf[n-1] == '\r' {
			r.buf = r.buf[:n-1]
			size--
		}
		if len(r.buf) > r.opt.MaxLine {
			// Exactly MaxLine+1 bytes and no "\r".
			over = true
			r.goOver()
		}
	} else if last == '\r' {
		size--
	}
	return Line{Data: r.buf, Size: size, Oversized: over}, nil
}
