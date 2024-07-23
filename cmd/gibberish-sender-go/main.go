package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/database64128/gibberish-sender-go"
)

var (
	logLevel      slog.Level
	network       string
	endpoint      string
	duration      time.Duration
	retryInterval time.Duration
	packetSize    int
	txSpeedMbps   int
	concurrency   int
)

func init() {
	flag.TextVar(&logLevel, "logLevel", slog.LevelInfo, "Log level")
	flag.StringVar(&network, "network", "tcp", "Endpoint network. Accepts: tcp, tcp4, tcp6, udp, udp4, udp6")
	flag.StringVar(&endpoint, "endpoint", "", "Endpoint in host:port")
	flag.DurationVar(&duration, "duration", 0, "Duration for sending gibberish")
	flag.DurationVar(&retryInterval, "retryInterval", 0, "Duration to wait before retrying connection")
	flag.IntVar(&packetSize, "packetSize", 1452, "UDP payload size. Defaults to 1452. 1452 (UDP payload) + 8 (UDP header) + 40 (IPv6 header) = 1500 (Typical Ethernet MTU).")
	flag.IntVar(&txSpeedMbps, "txSpeedMbps", 0, "UDP transfer speed in Mbps.")
	flag.IntVar(&concurrency, "concurrency", 1, "Number of concurrent connections to use.")
}

func main() {
	flag.Parse()

	if endpoint == "" {
		badFlagValue("Missing -endpoint <host:port>.")
	}

	if retryInterval < 0 {
		badFlagValue("-retryInterval cannot be negative.")
	}

	if packetSize < 0 {
		badFlagValue("-packetSize cannot be negative.")
	}

	if txSpeedMbps < 0 {
		badFlagValue("-txSpeedMbps cannot be negative.")
	}

	if concurrency < 1 {
		badFlagValue("-concurrency must be at least 1.")
	}

	switch network {
	case "tcp", "tcp4", "tcp6":
	case "udp", "udp4", "udp6":
		if txSpeedMbps == 0 {
			badFlagValue("-txSpeedMbps is required for UDP endpoints.")
		}
	default:
		badFlagValue("Invalid -network.")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))

	var (
		ctx    context.Context
		cancel context.CancelFunc
	)

	if duration <= 0 {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), duration)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.LogAttrs(ctx, slog.LevelInfo, "Received exit signal", slog.Any("signal", sig))
		cancel()
	}()

	if txSpeedMbps == 0 {
		gibberish.NewTCPSender(net.Dialer{}, network, endpoint, retryInterval).RunParallel(ctx, logger, concurrency)
	} else {
		s, err := gibberish.NewThrottledSender(net.ListenConfig{}, net.Dialer{}, network, endpoint, packetSize, txSpeedMbps, retryInterval)
		if err != nil {
			logger.LogAttrs(ctx, slog.LevelError, "Failed to create throttled sender", slog.Any("error", err))
		}
		s.RunParallel(ctx, logger, concurrency)
	}
}

func badFlagValue(a ...any) {
	fmt.Fprintln(os.Stderr, a...)
	flag.Usage()
	os.Exit(1)
}
