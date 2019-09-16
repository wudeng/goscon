package main

import (
	crand "crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	mrand "math/rand"
	"net"
	"os"
	"time"

	"github.com/ejoy/goscon/scp"
	kcp "github.com/ejoy/kcp-go"
)

type ClientCase struct {
	connect   string
	doneWrite chan struct{}
}

// 10ms - 100ms 发一个包
func (cc *ClientCase) testEchoWrite(conn net.Conn, done chan<- error) {
	var i uint32
	for {
		i++
		sz := mrand.Intn(100) + 50
		fmt.Fprintf(os.Stderr, "send sz = %v\n", sz)
		buf := make([]byte, sz+2)
		binary.LittleEndian.PutUint16(buf[0:], uint16(sz))
		binary.LittleEndian.PutUint32(buf[2:], i)
		crand.Read(buf[6:])
		if _, err := conn.Write(buf[:sz+2]); err != nil {
			cc.doneWrite <- struct{}{}
			done <- err
			return
		}
		if i > uint32(optPackets) {
			cc.doneWrite <- struct{}{}
			done <- nil
			return
		}
		t := time.Duration(mrand.Intn(90) + 10)
		time.Sleep(t * time.Millisecond)
	}
}

// 10ms - 100ms 收一次包
func (cc *ClientCase) testEchoRead(conn net.Conn, done chan<- error) {
	rlen := make([]byte, 2)
	rbuf := make([]byte, optMaxPacket+8)
	for {
		select {
		case <-cc.doneWrite:
			done <- nil
			return
		default:
		}
		if _, err := io.ReadFull(conn, rlen); err != nil {
			done <- err
			return
		}
		sz := binary.LittleEndian.Uint16(rlen[:2])
		if _, err := io.ReadFull(conn, rbuf[:sz]); err != nil {
			done <- err
			return
		}
		i := binary.LittleEndian.Uint32(rbuf)
		if i > 0 {
			fmt.Fprintf(os.Stderr, "recv sz = %v\n", sz)
		}

		t := time.Duration(mrand.Intn(90) + 10)
		time.Sleep(t * time.Millisecond)
	}
}

func (cc *ClientCase) testN(conn *scp.Conn) error {
	done := make(chan error, 2)
	go cc.testEchoWrite(conn, done)
	go cc.testEchoRead(conn, done)

	for i := 0; i < 2; i++ {
		err := <-done
		if err != nil {
			return err
		}
	}
	return nil
}

func Dial(network, connect string) (net.Conn, error) {
	if network == "tcp" {
		return net.Dial(network, connect)
	} else {
		return kcp.DialWithOptions(connect, nil, fecData, fecParity)
	}
}

func (cc *ClientCase) Start() error {
	old, err := Dial(network, cc.connect)
	if err != nil {
		return err
	}
	defer old.Close()

	originConn := scp.Client(old, &scp.Config{TargetServer: "test1"})
	if err = cc.testN(originConn); err != nil {
		return err
	}

	return nil
}

func startEchoServer(laddr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", laddr)
	if err != nil {
		return nil, err
	}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				break
			}
			go func(c net.Conn) {
				defer c.Close()
				for i := 0; i < 10; i++ {
					buf := make([]byte, optMaxPacket+2)
					binary.LittleEndian.PutUint16(buf[0:], uint16(optMaxPacket))
					binary.LittleEndian.PutUint32(buf[2:], 0)
					crand.Read(buf[6:])
					c.Write(buf[:optMaxPacket+2])
				}
				buf := make([]byte, 65536)
				count := 0
				for {
					c.SetReadDeadline(time.Now().Add(10 * time.Second))
					nr, er := c.Read(buf)
					if er != nil {
						fmt.Fprintf(os.Stderr, "read error : %v, %+v\n", count, er)
						break
					}
					count++
					if nr > 0 {
						_, ew := c.Write(buf[0:nr])
						if ew != nil {
							break
						}
					}
				}
			}(conn)
		}
	}()
	return ln, nil
}

func testN(addr string) {
	ch := make(chan error, optConcurrent)
	for {
		for i := 0; i < optConcurrent; i++ {
			go func() {
				cc := &ClientCase{
					connect: addr,
				}
				cc.doneWrite = make(chan struct{}, 1)
				ch <- cc.Start()
			}()
		}

		for i := 0; i < optConcurrent; i++ {
			err := <-ch
			if err != nil {
				fmt.Fprintf(os.Stderr, "<%d>: %s\n", i, err.Error())
			}
		}
	}
}

var optConcurrent, optPackets, optMinPacket, optMaxPacket int
var optVerbose bool
var network string
var fecData, fecParity int

func main() {
	var echoServer string
	var sconServer string

	flag.IntVar(&optConcurrent, "concurrent", 1, "concurrent connections")
	flag.IntVar(&optPackets, "packets", 10000, "packets number")
	flag.IntVar(&optMinPacket, "min", 50, "min packet size")
	flag.IntVar(&optMaxPacket, "max", 65530, "max packet size")
	flag.StringVar(&echoServer, "startEchoServer", "", "start echo server")
	flag.StringVar(&sconServer, "sconServer", "127.0.0.1:1248", "connect to scon server")
	flag.BoolVar(&optVerbose, "verbose", false, "verbose")
	kcp := flag.NewFlagSet("kcp", flag.ExitOnError)
	kcp.IntVar(&fecData, "fec_data", 1, "FEC: number of shards to split the data into")
	kcp.IntVar(&fecParity, "fec_parity", 0, "FEC: number of parity shards")
	flag.Parse()

	args := flag.Args()

	if len(args) > 0 && args[0] == "kcp" {
		kcp.Parse(args[1:])
		network = "kcp"
	} else {
		network = "tcp"
	}

	if echoServer != "" {
		ln, err := startEchoServer(echoServer)
		if err != nil {
			fmt.Fprintf(os.Stderr, "start echo server: %s\n", err.Error())
			return
		}
		fmt.Fprintf(os.Stdout, "echo server: %s", ln.Addr())
		ch := make(chan bool, 0)
		ch <- true
		return
	}

	if sconServer != "" {
		testN(sconServer)
	}
}
