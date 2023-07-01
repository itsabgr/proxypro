package iobuf

import (
	"bytes"
	"io"
)

type reader struct {
	fetch func() ([]byte, error)
	buf   bytes.Buffer
}

func NewReader(fetch func() ([]byte, error)) io.Reader {
	return &reader{fetch, bytes.Buffer{}}
}
func (r *reader) Read(dst []byte) (int, error) {
	if r.buf.Len() == 0 {
		data, err := r.fetch()
		if err != nil {
			return 0, err
		}
		if len(data) == 0 {
			return 0, io.EOF
		}
		r.buf.Write(data)
	}
	n, _ := r.buf.Read(dst)
	return n, nil
}
