package main

//go:generate protoc  --go_out=. --go-grpc_out=. grpc.proto
import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"github.com/itsabgr/proxypro/internal/iobuf"
	"github.com/itsabgr/proxypro/internal/proto"
	"github.com/pion/dtls/v2/pkg/crypto/selfsign"
	"github.com/sagernet/sing/common/x/constraints"
	"google.golang.org/grpc"
	"io"
	"net"
	"net/netip"
	"runtime"
	"time"
	"unicode/utf8"
)

func must[R any](r R, e error) R {
	if e != nil {
		panic(e)
	}
	return r
}

type service struct {
	proto.UnimplementedGRPCServer
}

func readN[N constraints.Integer](reader io.Reader, n N) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(reader, b)
	return b, err
}
func readAddr(typ byte, peer io.Reader) (addr string, err error) {
	switch typ {
	case 0x01:
		var addr []byte
		if addr, err = readN(peer, 4+2+2); err != nil {
			return "", err
		}
		port := binary.BigEndian.Uint16(addr[4:])
		ipAddr, _ := netip.AddrFromSlice(addr[:4])
		return netip.AddrPortFrom(ipAddr, port).String(), nil
	case 0x04:
		var addr []byte
		if addr, err = readN(peer, 16+2+2); err != nil {
			return "", err
		}
		port := binary.BigEndian.Uint16(addr[16:])
		ipAddr, _ := netip.AddrFromSlice(addr[:16])
		return netip.AddrPortFrom(ipAddr, port).String(), nil
	case 0x03:
		var addr []byte
		var addrLen byte
		if addr, err = readN(peer, 1); err != nil {
			return "", err
		}
		addrLen = addr[0]
		if addr, err = readN(peer, addrLen+2+2); err != nil {
			return "", err
		}
		port := binary.BigEndian.Uint16(addr[addrLen:])
		domain := string(addr[:addrLen])
		if !utf8.ValidString(domain) {
			return "", errors.New("invalid domain")
		}
		return fmt.Sprintf("%s:%d", domain, port), nil
	default:
		return "", errors.New("unknown addr type")
	}
}
func handleTCP(ctx context.Context, peer io.ReadWriter, header [2]byte) (err error) {
	addr, err := readAddr(header[1], peer)
	if err != nil {
		return err
	}
	var conn net.Conn
	switch header[1] {
	case 0x01:
		conn, err = net.DialTimeout("tcp4", addr, time.Second*5)
		if err != nil {
			return err
		}
	case 0x03:
		conn, err = net.DialTimeout("tcp", addr, time.Second*5)
		if err != nil {
			return err
		}
	case 0x04:
		conn, err = net.DialTimeout("tcp6", addr, time.Second*5)
		if err != nil {
			return err
		}
	default:
		panic("unreachable")
	}
	defer func() { _ = conn.Close() }()
	return pipe(ctx, peer, conn)
}

func handleTrojan(ctx context.Context, peer io.ReadWriter) (err error) {
	if _, err = readN(peer, 56); err != nil {
		return err
	}
	if _, err = readN(peer, 2); err != nil {
		return err
	}
	var header [2]byte
	if _, err := io.ReadFull(peer, header[:]); err != nil {
		return err
	}
	switch header[0] {
	case 0x01:
		return handleTCP(ctx, peer, header)
	case 0x03:
		return errors.New("udp not supported")
	default:
		return errors.New("unknown cmd")
	}

}
func async(do func() error) <-chan error {
	c := make(chan error)
	go func() {
		c <- do()
	}()
	return c
}
func pipe(ctx context.Context, a, b io.ReadWriter) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case err := <-async(func() error {
		return copyStreams(ctx, a, b)
	}):
		return err
	case err := <-async(func() error {
		return copyStreams(ctx, b, a)
	}):
		return err
	}
}

func copyStreams(ctx context.Context, dst io.Writer, src io.Reader) error {
	buf := make([]byte, 1024)
	for {
		if ec := ctx.Err(); ec != nil {
			return ec
		}
		nr, er := src.Read(buf)
		if er != nil && er != io.EOF {
			return er
		}
		if nr > 0 {
			_, ew := dst.Write(buf[:nr])
			if ew != nil {
				return ew
			}
		}
		runtime.Gosched()
	}
}

func (s *service) Tun(inputStream proto.GRPC_TunServer) error {
	ctx, cancel := context.WithCancel(inputStream.Context())
	defer cancel()
	writer := iobuf.NewWriter(func(b []byte) (int, error) {
		if err := inputStream.Send(&proto.Hunk{Data: b}); err != nil {
			return 0, err
		}
		return len(b), nil
	})
	reader := iobuf.NewReader(func() ([]byte, error) {
		hunk, err := inputStream.Recv()
		if err != nil {
			return nil, err
		}
		return hunk.Data, nil
	})
	err := handleTrojan(inputStream.Context(), iobuf.NewDuplex(ctx, reader, writer))
	return err
}

var flagAddr = flag.String("addr", "", "")

func main() {
	flag.Parse()
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{must(selfsign.GenerateSelfSigned())},
		NextProtos:   []string{"h2"},
	}
	ln := must(tls.Listen("tcp", *flagAddr, tlsConfig))
	defer func() { _ = ln.Close() }()
	grpcServer := grpc.NewServer()
	proto.RegisterGRPCServer(grpcServer, &service{})
	panic(grpcServer.Serve(ln))
}
