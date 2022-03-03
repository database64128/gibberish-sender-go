package main

import (
	"crypto/rand"
	"errors"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	var endpoint string
	var duration int
	var suppressTimestamps bool

	flag.StringVar(&endpoint, "endpoint", "", "TCP endpoint in address:port")
	flag.IntVar(&duration, "duration", 0, "Duration for sending gibberish in seconds")
	flag.BoolVar(&suppressTimestamps, "suppressTimestamps", false, "Specify this flag to omit timestamps in logs")

	flag.Parse()

	if suppressTimestamps {
		log.SetFlags(log.Flags() &^ (log.Ldate | log.Ltime))
	}

	conn, err := net.Dial("tcp", endpoint)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	startTime := time.Now()

	if duration > 0 {
		conn.SetDeadline(startTime.Add(time.Duration(duration) * time.Second))
	}

	log.Printf("Started gibberish sender to %s for %d seconds", endpoint, duration)

	sigs := make(chan os.Signal)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigs
		log.Printf("Received %s, stopping...", sig.String())
		conn.SetDeadline(time.Now())
	}()

	n, err := conn.(*net.TCPConn).ReadFrom(rand.Reader)

	log.Printf("%d bytes sent in %s", n, time.Since(startTime))
	if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
		log.Print(err)
	}
}
