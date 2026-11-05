package network

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"time"

	"github.com/artemlavr/giocondo/internal/model/panel"
	"github.com/artemlavr/giocondo/internal/service/operation"
	"github.com/artemlavr/giocondo/pkg/logger"
)

func operationID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}

	return hex.EncodeToString(b)
}

func (s *Service) saveStage(ctx context.Context, o *panel.NetworkOperation, stage string) error {
	o.Stage = stage
	o.Log += stage + "\n"

	return s.network.SaveNetworkOperation(ctx, o)
}

// finishOperation records the outcome of a background network operation.
func (s *Service) finishOperation(ctx context.Context, o *panel.NetworkOperation, err error) {
	end := s.hooks.Now()

	o.FinishedAt = &end
	if err != nil {
		o.Status = panel.OperationFailed
		o.Error = operation.SafeError(err)
	} else {
		o.Status = panel.OperationOK
		o.Error = ""
	}

	// Persist final state even when the worker context was cancelled.
	persist, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()

	if saveErr := s.network.SaveNetworkOperation(persist, o); saveErr != nil {
		logger.FromCtx(ctx).Error("save IP operation", "operation", o.ID, "err", saveErr)
	}
}

func (s *Service) verifyDNS(ctx context.Context, host, ip string) error {
	records, err := s.dns.Lookup(ctx, host)
	if err != nil {
		return err
	}

	if len(records) != 1 || records[0].Content != ip || records[0].Type != "A" ||
		records[0].Proxied || records[0].TTL != 60 {
		return errors.New("DNS update was not confirmed")
	}

	if s.resolveA != nil {
		return s.resolveA(ctx, host, ip)
	}

	return nil
}

// drain gives clients time to move to the new address before the old installation is revoked.
func (s *Service) drain(ctx context.Context) error {
	delay := s.drainDelay
	if delay <= 0 {
		delay = DefaultDrainDelay
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// sourceSSH targets the old address unless the supplied credentials name another host.
func sourceSSH(in *panel.SSHInput, oldIP string) panel.SSHInput {
	source := panel.SSHInput{Host: oldIP}
	if in != nil {
		source = *in
		if source.Host == "" {
			source.Host = oldIP
		}
	}

	return source
}

// DefaultDrainDelay is how long clients get to move to the new address before the old installation is
// revoked.
const DefaultDrainDelay = 120 * time.Second
