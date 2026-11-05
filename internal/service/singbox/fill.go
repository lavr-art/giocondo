package singbox

import (
	"fmt"

	"github.com/artemlavr/giocondo/internal/model/protocol"
)

// Fill adds missing secrets: Reality keys and short_id, the self-signed Hysteria2
// certificate, WireGuard server keys, user UUIDs, their WG keys and addresses.
// Values already set are not changed, so a repeated call touches nothing.
func Fill(n *protocol.Node, users []protocol.User) error {
	for i := range n.Inbounds {
		if err := fillInbound(n.Host, &n.Inbounds[i]); err != nil {
			return err
		}
	}

	for i := range users {
		if users[i].UUID == "" {
			id, err := protocol.NewUUID()
			if err != nil {
				return err
			}

			users[i].UUID = id
		}
	}

	wg := n.WireGuardInbound()
	if wg == nil {
		return nil
	}

	var taken []string

	for _, u := range users {
		if u.WG != nil && u.WG.Address != "" {
			taken = append(taken, u.WG.Address)
		}
	}

	for i := range users {
		var err error
		if taken, err = fillUserWG(&users[i], wg.WireGuard.WGSubnet(), taken); err != nil {
			return err
		}
	}

	return nil
}

func fillInbound(host string, in *protocol.Inbound) error {
	switch in.Type {
	case protocol.TypeVLESSReality:
		if in.Reality == nil {
			in.Reality = &protocol.RealityParams{}
		}

		return fillReality(in.Reality)
	case protocol.TypeHysteria2:
		if in.Hysteria2 == nil {
			in.Hysteria2 = &protocol.Hysteria2Params{}
		}

		p := in.Hysteria2
		if p.TLSMode() != protocol.TLSSelfSigned || (p.CertPEM != "" && p.KeyPEM != "") {
			return nil
		}

		if host == "" {
			return fmt.Errorf("inbound %s: node host is required to issue a certificate", in.EffectiveTag())
		}

		cert, key, err := SelfSignedCert(host)
		if err != nil {
			return err
		}

		p.CertPEM, p.KeyPEM = cert, key
	case protocol.TypeWireGuard:
		if in.WireGuard == nil {
			in.WireGuard = &protocol.WireGuardParams{}
		}

		if in.WireGuard.PrivateKey == "" {
			priv, _, err := protocol.NewWGKeypair()
			if err != nil {
				return err
			}

			in.WireGuard.PrivateKey = priv
		}
	}

	return nil
}

func fillReality(r *protocol.RealityParams) error {
	if r.PrivateKey == "" {
		priv, _, err := protocol.NewRealityKeypair()
		if err != nil {
			return err
		}

		r.PrivateKey = priv
	}

	if len(r.ShortIDs) == 0 {
		id, err := protocol.NewShortID()
		if err != nil {
			return err
		}

		r.ShortIDs = []string{id}
	}

	return nil
}

// fillUserWG completes a user's WireGuard peer and returns taken with the newly allocated address.
func fillUserWG(u *protocol.User, subnet string, taken []string) ([]string, error) {
	if u.WG == nil {
		u.WG = &protocol.WGPeer{}
	}

	if u.WG.PrivateKey == "" && u.WG.PublicKey == "" {
		priv, pub, err := protocol.NewWGKeypair()
		if err != nil {
			return taken, err
		}

		u.WG.PrivateKey, u.WG.PublicKey = priv, pub
	} else if u.WG.PublicKey == "" {
		pub, err := protocol.WGPublicKey(u.WG.PrivateKey)
		if err != nil {
			return taken, fmt.Errorf("user %s: %w", u.Name, err)
		}

		u.WG.PublicKey = pub
	}

	if u.WG.PresharedKey == "" {
		psk, err := protocol.NewPresharedKey()
		if err != nil {
			return taken, err
		}

		u.WG.PresharedKey = psk
	}

	if u.WG.Address == "" {
		addr, err := AllocateWGAddress(subnet, taken)
		if err != nil {
			return taken, fmt.Errorf("user %s: %w", u.Name, err)
		}

		u.WG.Address = addr
		taken = append(taken, addr)
	}

	return taken, nil
}
