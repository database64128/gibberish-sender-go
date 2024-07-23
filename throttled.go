package gibberish

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/netip"
	"os"
	"strconv"
	"sync"
	"time"
)

// When there's a transfer speed limit,
// wakes up 5 times a second (every 200ms) to send data.
const wakeupFrequency = 5

// throttledSend executes the send function at the specified speed.
func throttledSend(ctx context.Context, packetSize, txSpeedMbps int, send func(b []byte) (int, error)) (bytesSent uint64, err error) {
	// Wakes up {wakeupFrequency} times per second.
	bytesPerWake := txSpeedMbps * 1000 * 1000 / 8 / wakeupFrequency
	fullSizePacketsPerWake := bytesPerWake / packetSize
	lastSmallPacketSizePerWake := bytesPerWake % packetSize

	r := rand.NewPCG(rand.Uint64(), rand.Uint64())
	b := make([]byte, bytesPerWake)
	ticker := time.NewTicker(time.Second / wakeupFrequency)

	go func() {
		<-ctx.Done()
		ticker.Stop()
	}()

	for {
		pcgFillBytes(r, b)

		select {
		case <-ticker.C:
		case <-ctx.Done():
			return
		}

		// Send full-size packets.
		for i := 0; i < fullSizePacketsPerWake; i++ {
			var n int
			n, err = send(b[i*packetSize : (i+1)*packetSize])
			bytesSent += uint64(n)
			if err != nil {
				return
			}
		}

		// Send the last small packet.
		if lastSmallPacketSizePerWake > 0 {
			var n int
			n, err = send(b[fullSizePacketsPerWake*packetSize:])
			bytesSent += uint64(n)
			if err != nil {
				return
			}
		}
	}
}

// ThrottledSender sends gibberish to a TCP or UDP endpoint at a specified speed.
type ThrottledSender struct {
	listenConfig  net.ListenConfig
	addrPort      netip.AddrPort
	dialer        net.Dialer
	network       string
	address       string
	packetSize    int
	txSpeedMbps   int
	retryInterval time.Duration
}

// NewThrottledSender creates a new ThrottledSender.
func NewThrottledSender(listenConfig net.ListenConfig, dialer net.Dialer, network, address string, packetSize, txSpeedMbps int, retryInterval time.Duration) (*ThrottledSender, error) {
	switch network {
	case "tcp", "tcp4", "tcp6":
		return &ThrottledSender{
			dialer:        dialer,
			network:       network,
			address:       address,
			txSpeedMbps:   txSpeedMbps,
			retryInterval: retryInterval,
		}, nil
	case "udp", "udp4", "udp6":
		addrPort, err := netip.ParseAddrPort(address)
		if err != nil {
			return nil, err
		}
		return &ThrottledSender{
			listenConfig: listenConfig,
			addrPort:     addrPort,
			network:      network,
			address:      address,
			packetSize:   packetSize,
			txSpeedMbps:  txSpeedMbps,
		}, nil
	default:
		return nil, fmt.Errorf("unsupported network: %s", network)
	}
}

func (s *ThrottledSender) newTCPConn(ctx context.Context) (*net.TCPConn, error) {
	c, err := s.dialer.DialContext(ctx, s.network, s.address)
	if err != nil {
		return nil, err
	}
	return c.(*net.TCPConn), nil
}

func (s *ThrottledSender) runTCP(ctx context.Context, logger *slog.Logger) {
	for {
		logger.LogAttrs(ctx, slog.LevelInfo, "Connecting to TCP endpoint", slog.String("address", s.address))

		c, err := s.newTCPConn(ctx)
		if err != nil {
			logger.LogAttrs(ctx, slog.LevelWarn, "Failed to connect to TCP endpoint",
				slog.String("address", s.address),
				slog.Any("error", err),
			)

			select {
			case <-ctx.Done():
				return
			case <-time.After(dialRetryInterval):
				continue
			}
		}

		logger.LogAttrs(ctx, slog.LevelInfo, "Connected to TCP endpoint", slog.String("address", s.address))

		writeFailed := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				c.SetDeadline(time.Now())
			case <-writeFailed:
			}
		}()

		bytesSent, err := throttledSend(ctx, 32768, s.txSpeedMbps, func(b []byte) (int, error) {
			return c.Write(b)
		})
		if err != nil {
			if !errors.Is(err, os.ErrDeadlineExceeded) {
				logger.LogAttrs(ctx, slog.LevelWarn, "Failed to write to TCP endpoint",
					slog.String("address", s.address),
					slog.Any("error", err),
				)
				close(writeFailed)
			}
			c.Close()
		}

		logger.LogAttrs(ctx, slog.LevelInfo, "Disconnected from TCP endpoint",
			slog.String("address", s.address),
			slog.Uint64("bytesSent", bytesSent),
		)

		select {
		case <-ctx.Done():
			return
		case <-time.After(s.retryInterval):
		}
	}
}

func (s *ThrottledSender) runUDP(ctx context.Context, logger *slog.Logger) {
	pc, err := s.listenConfig.ListenPacket(ctx, s.network, "")
	if err != nil {
		logger.LogAttrs(ctx, slog.LevelError, "Failed to create UDP socket", slog.Any("error", err))
		return
	}
	uc := pc.(*net.UDPConn)
	defer uc.Close()

	bytesSent, err := throttledSend(ctx, s.packetSize, s.txSpeedMbps, func(b []byte) (int, error) {
		return uc.WriteToUDPAddrPort(b, s.addrPort)
	})
	if err != nil && !errors.Is(err, os.ErrDeadlineExceeded) {
		logger.LogAttrs(ctx, slog.LevelWarn, "Failed to write to UDP endpoint",
			slog.String("address", s.address),
			slog.Any("error", err),
		)
	}

	logger.LogAttrs(ctx, slog.LevelInfo, "Finished sending to UDP endpoint",
		slog.String("address", s.address),
		slog.Uint64("bytesSent", bytesSent),
	)
}

// Run starts sending gibberish until the context is done.
func (s *ThrottledSender) Run(ctx context.Context, logger *slog.Logger) {
	switch s.network {
	case "tcp", "tcp4", "tcp6":
		s.runTCP(ctx, logger)
	case "udp", "udp4", "udp6":
		s.runUDP(ctx, logger)
	default:
		panic(fmt.Sprintf("unsupported network: %q", s.network))
	}
}

// RunParallel starts multiple sending goroutines that finish when the context is done.
func (s *ThrottledSender) RunParallel(ctx context.Context, logger *slog.Logger, concurrency int) {
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		logger := logger.WithGroup(strconv.Itoa(i))
		wg.Add(1)
		go func() {
			s.Run(ctx, logger)
			wg.Done()
		}()
	}
	wg.Wait()
}
