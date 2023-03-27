package gibberish

import (
	"context"
	"errors"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/database64128/gibberish-sender-go/fastrand"
	"go.uber.org/zap"
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
func (s *TCPSender) Run(ctx context.Context, logger *zap.Logger) {
	r := fastrand.New()
	b := make([]byte, 32768)

	for {
		logger.Info("Connecting to TCP endpoint", zap.String("address", s.address))

		c, err := s.newTCPConn(ctx)
		if err != nil {
			logger.Warn("Failed to connect to TCP endpoint",
				zap.String("address", s.address),
				zap.Error(err),
			)

			select {
			case <-ctx.Done():
				return
			case <-time.After(dialRetryInterval):
				continue
			}
		}

		logger.Info("Connected to TCP endpoint", zap.String("address", s.address))

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
			r.Fill(b)

			n, err := c.Write(b)
			bytesSent += uint64(n)
			if err != nil {
				if errors.Is(err, os.ErrDeadlineExceeded) {
					c.Close()
					break
				}
				logger.Warn("Failed to write to TCP endpoint",
					zap.String("address", s.address),
					zap.Error(err),
				)
				close(writeFailed)
				c.Close()
				break
			}
		}

		logger.Info("Disconnected from TCP endpoint",
			zap.String("address", s.address),
			zap.Uint64("bytesSent", bytesSent),
		)

		select {
		case <-ctx.Done():
			return
		case <-time.After(s.retryInterval):
		}
	}
}

// RunParallel starts multiple sending goroutines that finish when the context is done.
func (s *TCPSender) RunParallel(ctx context.Context, logger *zap.Logger, concurrency int) {
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		logger := logger.Named(strconv.Itoa(i))
		wg.Add(1)
		go func() {
			s.Run(ctx, logger)
			wg.Done()
		}()
	}
	wg.Wait()
}
