package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// When there's a transfer speed limit,
// wakes up 5 times a second (every 200ms) to send data.
const wakeupFrequency = 5

func main() {
	var network string
	var endpoint string
	var duration time.Duration
	var packetSize int
	var txSpeedMbps int
	var suppressTimestamps bool

	flag.StringVar(&network, "network", "tcp", "Endpoint network. Accepts: tcp, tcp4, tcp6, udp, udp4, udp6")
	flag.StringVar(&endpoint, "endpoint", "", "Endpoint in host:port")
	flag.DurationVar(&duration, "duration", 0, "Duration for sending gibberish")
	flag.IntVar(&packetSize, "packetSize", 1452, "UDP payload size. Defaults to 1452. 1452 (UDP payload) + 8 (UDP header) + 40 (IPv6 header) = 1500 (Typical Ethernet MTU).")
	flag.IntVar(&txSpeedMbps, "txSpeedMbps", 0, "UDP transfer speed in Mbps.")
	flag.BoolVar(&suppressTimestamps, "suppressTimestamps", false, "Specify this flag to omit timestamps in logs")

	flag.Parse()

	if suppressTimestamps {
		log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))
	}

	// Validate flags
	if endpoint == "" {
		fmt.Println("Missing -endpoint <host:port>.")
		flag.Usage()
		return
	}

	switch network {
	case "tcp", "tcp4", "tcp6":
	default:
		if txSpeedMbps == 0 {
			fmt.Println("-txSpeedMbps is required for non-TCP endpoints.")
			flag.Usage()
			return
		}
	}

	// Open sockets
	var conn net.Conn
	var err error

	switch network {
	case "udp", "udp4", "udp6":
		conn, err = net.ListenUDP(network, nil)
	default:
		conn, err = net.Dial(network, endpoint)
	}

	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	// Resolve UDPAddr
	var udpaddr *net.UDPAddr

	switch network {
	case "udp", "udp4", "udp6":
		udpaddr, err = net.ResolveUDPAddr(network, endpoint)
		if err != nil {
			log.Fatal(err)
		}
	}

	// Timer and duration
	startTime := time.Now()
	var durationLog string

	if duration > 0 {
		conn.SetDeadline(startTime.Add(duration))
		durationLog = fmt.Sprintf(" for %s", duration.String())
	}

	log.Printf("Started gibberish sender to %s%s", endpoint, durationLog)

	// Handle cancellation
	sigs := make(chan os.Signal)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Printf("Received %s, stopping...", sig.String())
		conn.SetDeadline(time.Now())
	}()

	// Start sending
	var bytesSent int64

	if c, ok := conn.(*net.TCPConn); ok && txSpeedMbps == 0 {
		bytesSent, err = c.ReadFrom(rand.Reader)
	} else {
		// Wakes up {wakeupFrequency} times per second.
		bytesPerWake := txSpeedMbps * 1000 * 1000 / 8 / wakeupFrequency
		fullSizePacketsPerWake := bytesPerWake / packetSize
		lastSmallPacketSizePerWake := bytesPerWake % packetSize

		b := make([]byte, bytesPerWake)
		ch := make(chan struct{}, wakeupFrequency)

		go func() {
			for {
				ch <- struct{}{}
				time.Sleep(time.Second / wakeupFrequency)
			}
		}()

	wake:
		for {
			_, err = rand.Read(b)
			if err != nil {
				log.Fatal(err)
			}

			<-ch

			// Send full-size packets
			for i := 0; i < fullSizePacketsPerWake; i++ {
				var n int
				n, err = sendPacket(conn, b[i*packetSize:(i+1)*packetSize], udpaddr)
				bytesSent += int64(n)
				if err != nil {
					break wake
				}
			}

			// Send the last small packet
			if lastSmallPacketSizePerWake > 0 {
				var n int
				n, err = sendPacket(conn, b[fullSizePacketsPerWake*packetSize:], udpaddr)
				bytesSent += int64(n)
				if err != nil {
					break wake
				}
			}
		}
	}

	log.Printf("%d bytes sent in %s", bytesSent, time.Since(startTime))
	if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
		log.Print(err)
	}
}

func sendPacket(conn net.Conn, b []byte, addr *net.UDPAddr) (n int, err error) {
	switch c := conn.(type) {
	case *net.UDPConn:
		n, err = c.WriteToUDP(b, addr)
	default:
		n, err = c.Write(b)
	}

	return
}
