package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/database64128/gibberish-sender-go"
	"github.com/database64128/gibberish-sender-go/jsonhelper"
	"github.com/database64128/gibberish-sender-go/logging"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	zapConf       = flag.String("zapConf", "", "Preset name or path to JSON configuration file for building the zap logger.\nAvailable presets: console (default), systemd, production, development")
	logLevel      = flag.String("logLevel", "", "Override the logger configuration's log level.\nAvailable levels: debug, info, warn, error, dpanic, panic, fatal")
	network       = flag.String("network", "tcp", "Endpoint network. Accepts: tcp, tcp4, tcp6, udp, udp4, udp6")
	endpoint      = flag.String("endpoint", "", "Endpoint in host:port")
	duration      = flag.Duration("duration", 0, "Duration for sending gibberish")
	retryInterval = flag.Duration("retryInterval", 0, "Duration to wait before retrying connection")
	packetSize    = flag.Int("packetSize", 1452, "UDP payload size. Defaults to 1452. 1452 (UDP payload) + 8 (UDP header) + 40 (IPv6 header) = 1500 (Typical Ethernet MTU).")
	txSpeedMbps   = flag.Int("txSpeedMbps", 0, "UDP transfer speed in Mbps.")
	concurrency   = flag.Int("concurrency", 1, "Number of concurrent connections to use.")
)

func main() {
	flag.Parse()

	if *endpoint == "" {
		badFlagValue("Missing -endpoint <host:port>.")
	}

	if *retryInterval < 0 {
		badFlagValue("-retryInterval cannot be negative.")
	}

	if *packetSize < 0 {
		badFlagValue("-packetSize cannot be negative.")
	}

	if *txSpeedMbps < 0 {
		badFlagValue("-txSpeedMbps cannot be negative.")
	}

	if *concurrency < 1 {
		badFlagValue("-concurrency must be at least 1.")
	}

	switch *network {
	case "tcp", "tcp4", "tcp6":
	case "udp", "udp4", "udp6":
		if *txSpeedMbps == 0 {
			badFlagValue("-txSpeedMbps is required for UDP endpoints.")
		}
	default:
		badFlagValue("Invalid -network.")
	}

	var zc zap.Config

	switch *zapConf {
	case "console", "":
		zc = logging.NewProductionConsoleConfig(false)
	case "systemd":
		zc = logging.NewProductionConsoleConfig(true)
	case "production":
		zc = zap.NewProductionConfig()
	case "development":
		zc = zap.NewDevelopmentConfig()
	default:
		if err := jsonhelper.LoadAndDecodeDisallowUnknownFields(*zapConf, &zc); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}

	if *logLevel != "" {
		l, err := zapcore.ParseLevel(*logLevel)
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		zc.Level.SetLevel(l)
	}

	logger, err := zc.Build()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	defer logger.Sync()

	var (
		ctx    context.Context
		cancel context.CancelFunc
	)

	if *duration <= 0 {
		ctx, cancel = context.WithCancel(context.Background())
	} else {
		ctx, cancel = context.WithTimeout(context.Background(), *duration)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		logger.Info("Received exit signal", zap.Stringer("signal", sig))
		cancel()
	}()

	if *txSpeedMbps == 0 {
		gibberish.NewTCPSender(net.Dialer{}, *network, *endpoint, *retryInterval).RunParallel(ctx, logger, *concurrency)
	} else {
		s, err := gibberish.NewThrottledSender(net.ListenConfig{}, net.Dialer{}, *network, *endpoint, *packetSize, *txSpeedMbps, *retryInterval)
		if err != nil {
			logger.Fatal("Failed to create throttled sender", zap.Error(err))
		}
		s.RunParallel(ctx, logger, *concurrency)
	}
}

func badFlagValue(msg string) {
	fmt.Println(msg)
	flag.Usage()
	os.Exit(1)
}
