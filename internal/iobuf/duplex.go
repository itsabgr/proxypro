package iobuf

import (
	"io"
)

type duplex struct {
	r io.Reader
	w io.Writer
}

func (d *duplex) Read(p []byte) (int, error) {
	return d.r.Read(p)
}

func (d *duplex) Write(p []byte) (int, error) {
	return d.w.Write(p)
}

func NewDuplex(r io.Reader, w io.Writer) io.ReadWriter {
	return &duplex{r, w}
}
