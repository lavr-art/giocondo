// Package nodeauth extracts verified gRPC client identities.
package nodeauth

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

var nodeNameRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// NodeName returns the CN of the verified client certificate from the gRPC call context.
func NodeName(ctx context.Context) (string, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return "", errors.New("no peer")
	}

	info, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return "", errors.New("connection is not TLS")
	}

	chains := info.State.VerifiedChains
	if len(chains) == 0 || len(chains[0]) == 0 {
		return "", errors.New("no verified client certificate")
	}

	cn := chains[0][0].Subject.CommonName
	if !nodeNameRe.MatchString(cn) {
		return "", fmt.Errorf("bad node name in certificate: %q", cn)
	}

	return cn, nil
}
