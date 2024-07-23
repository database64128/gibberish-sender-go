package gibberish

import (
	"context"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net"
	"os"
	"strconv"
	"sync"
	"time"
)

const dialRetryInterval = 1 * time.Second

// TCPSender sends gibberish to a TCP endpoint at full speed.
type TCPSender struct {
	dialer        net.Dialer
	network       string
	address       string
	retryInterval time.Duration
}

// NewTCPSender creates a new TCPSender.
func NewTCPSender(dialer net.Dialer, network, address string, retryInterval time.Duration) *TCPSender {
	return &TCPSender{
		dialer:        dialer,
		network:       network,
		address:       address,
		retryInterval: retryInterval,
	}
}

func (s *TCPSender) newTCPConn(ctx context.Context) (*net.TCPConn, error) {
	c, err := s.dialer.DialContext(ctx, s.network, s.address)
	if err != nil {
		return nil, err
	}
	return c.(*net.TCPConn), nil
}

// Run starts sending gibberish until the context is done.
func (s *TCPSender) Run(ctx context.Context, logger *slog.Logger) {
	r := rand.NewPCG(rand.Uint64(), rand.Uint64())
	b := make([]byte, 32768)

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

		var bytesSent uint64

		for {
			pcgFillBytes(r, b)

			n, err := c.Write(b)
			bytesSent += uint64(n)
			if err != nil {
				if errors.Is(err, os.ErrDeadlineExceeded) {
					c.Close()
					break
				}
				logger.LogAttrs(ctx, slog.LevelWarn, "Failed to write to TCP endpoint",
					slog.String("address", s.address),
					slog.Any("error", err),
				)
				close(writeFailed)
				c.Close()
				break
			}
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

// RunParallel starts multiple sending goroutines that finish when the context is done.
func (s *TCPSender) RunParallel(ctx context.Context, logger *slog.Logger, concurrency int) {
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
