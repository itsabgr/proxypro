package iobuf

import (
	"io"
)

type writer func([]byte) (int, error)

func (w writer) Write(p []byte) (n int, err error) {
	return w(p)
}

func NewWriter(out func([]byte) (int, error)) io.Writer {
	return writer(out)
}
