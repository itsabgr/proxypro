package iobuf

import (
	"context"
	"io"
)

type duplex struct {
	r   io.Reader
	w   io.Writer
	ctx context.Context
}

func (d *duplex) Read(p []byte) (int, error) {
	if err := d.ctx.Err(); err != nil {
		return 0, err
	}
	return d.r.Read(p)
}

func (d *duplex) Write(p []byte) (int, error) {
	if err := d.ctx.Err(); err != nil {
		return 0, err
	}
	return d.w.Write(p)
}

func NewDuplex(ctx context.Context, r io.Reader, w io.Writer) io.ReadWriter {
	return &duplex{r, w, ctx}
}
