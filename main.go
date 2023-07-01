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
	"google.golang.org/grpc/credentials"
	"io"
	"log"
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

func ReadN[N constraints.Integer](reader io.Reader, n N) ([]byte, error) {
	b := make([]byte, n)
	_, err := io.ReadFull(reader, b)
	return b, err
}
func readAddr(typ byte, peer io.Reader) (addr string, err error) {
	switch typ {
	case 0x01:
		var addr []byte
		if addr, err = ReadN(peer, 4+2+2); err != nil {
			return "", err
		}
		port := binary.BigEndian.Uint16(addr[4:])
		ipAddr, _ := netip.AddrFromSlice(addr[:4])
		return netip.AddrPortFrom(ipAddr, port).String(), nil
	case 0x04:
		var addr []byte
		if addr, err = ReadN(peer, 16+2+2); err != nil {
			return "", err
		}
		port := binary.BigEndian.Uint16(addr[16:])
		ipAddr, _ := netip.AddrFromSlice(addr[:16])
		return netip.AddrPortFrom(ipAddr, port).String(), nil
	case 0x03:
		var addr []byte
		var addrLen byte
		if addr, err = ReadN(peer, 1); err != nil {
			return "", err
		}
		addrLen = addr[0]
		if addr, err = ReadN(peer, addrLen+2+2); err != nil {
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
	conn, err := net.DialTimeout("tcp", addr, time.Second*5)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	return Pipe(ctx, peer, conn)
}

func handleTrojan(ctx context.Context, peer io.ReadWriter) (err error) {
	if _, err = ReadN(peer, 56); err != nil {
		return err
	}
	if _, err = ReadN(peer, 2); err != nil {
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

func Pipe(parent context.Context, a, b io.ReadWriter) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func() {
		defer cancel()
		_ = pipe(ctx, a, b, make([]byte, 1024))
	}()
	go func() {
		defer cancel()
		_ = pipe(ctx, b, a, make([]byte, 1024))
	}()
	<-ctx.Done()
	return ctx.Err()
}

func pipe(ctx context.Context, dst io.Writer, src io.Reader, buf []byte) error {
	for {
		if ec := ctx.Err(); ec != nil {
			return ec
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			_, ew := dst.Write(buf[:nr])
			if ew != nil {
				return ew
			}
		}
		if er != nil && er != io.EOF {
			return er
		}
		runtime.Gosched()
	}
}

func (s *service) TunMulti(inputStream proto.GRPC_TunMultiServer) error {
	writer := iobuf.NewWriter(func(b []byte) (int, error) {
		if err := inputStream.Send(&proto.MultiHunk{Data: [][]byte{b}}); err != nil {
			return 0, err
		}
		return len(b), nil
	})
	reader := iobuf.NewReader(func() ([][]byte, error) {
		hunk, err := inputStream.Recv()
		if err != nil {
			return nil, err
		}
		return hunk.Data, nil
	})
	err := handleTrojan(inputStream.Context(), iobuf.NewDuplex(reader, writer))
	log.Println(err)
	return err
}
func (s *service) Tun(inputStream proto.GRPC_TunServer) error {
	writer := iobuf.NewWriter(func(b []byte) (int, error) {
		if err := inputStream.Send(&proto.Hunk{Data: b}); err != nil {
			return 0, err
		}
		return len(b), nil
	})
	reader := iobuf.NewReader(func() ([][]byte, error) {
		hunk, err := inputStream.Recv()
		if err != nil {
			return nil, err
		}
		return [][]byte{hunk.Data}, nil
	})
	err := handleTrojan(inputStream.Context(), iobuf.NewDuplex(reader, writer))
	log.Println(err)
	return err
}

var flagAddr = flag.String("addr", "", "")

func main() {
	flag.Parse()
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{must(selfsign.GenerateSelfSigned())},
		NextProtos:   []string{"h2"},
	}
	ln := must(net.Listen("tcp", *flagAddr))
	defer func() { _ = ln.Close() }()
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(tlsConfig)))
	proto.RegisterGRPCServer(server, &service{})
	panic(server.Serve(ln))
}
