package singbox

import "fmt"

// Fill adds missing secrets: Reality keys and short_id, the self-signed Hysteria2
// certificate, WireGuard server keys, user UUIDs, their WG keys and addresses.
// Values already set are not changed, so a repeated call touches nothing.
func Fill(n *Node, users []User) error {
	for i := range n.Inbounds {
		in := &n.Inbounds[i]
		switch in.Type {
		case TypeVLESSReality:
			if in.Reality == nil {
				in.Reality = &RealityParams{}
			}
			if in.Reality.PrivateKey == "" {
				priv, _, err := NewRealityKeypair()
				if err != nil {
					return err
				}
				in.Reality.PrivateKey = priv
			}
			if len(in.Reality.ShortIDs) == 0 {
				id, err := NewShortID()
				if err != nil {
					return err
				}
				in.Reality.ShortIDs = []string{id}
			}
		case TypeHysteria2:
			if in.Hysteria2 == nil {
				in.Hysteria2 = &Hysteria2Params{}
			}
			p := in.Hysteria2
			if p.TLSMode() == TLSSelfSigned && (p.CertPEM == "" || p.KeyPEM == "") {
				if n.Host == "" {
					return fmt.Errorf("inbound %s: node host is required to issue a certificate", in.EffectiveTag())
				}
				cert, key, err := SelfSignedCert(n.Host)
				if err != nil {
					return err
				}
				p.CertPEM, p.KeyPEM = cert, key
			}
		case TypeWireGuard:
			if in.WireGuard == nil {
				in.WireGuard = &WireGuardParams{}
			}
			if in.WireGuard.PrivateKey == "" {
				priv, _, err := NewWGKeypair()
				if err != nil {
					return err
				}
				in.WireGuard.PrivateKey = priv
			}
		}
	}

	for i := range users {
		if users[i].UUID == "" {
			id, err := NewUUID()
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
		u := &users[i]
		if u.WG == nil {
			u.WG = &WGPeer{}
		}
		if u.WG.PrivateKey == "" && u.WG.PublicKey == "" {
			priv, pub, err := NewWGKeypair()
			if err != nil {
				return err
			}
			u.WG.PrivateKey, u.WG.PublicKey = priv, pub
		} else if u.WG.PublicKey == "" {
			pub, err := WGPublicKey(u.WG.PrivateKey)
			if err != nil {
				return fmt.Errorf("user %s: %w", u.Name, err)
			}
			u.WG.PublicKey = pub
		}
		if u.WG.PresharedKey == "" {
			psk, err := NewPresharedKey()
			if err != nil {
				return err
			}
			u.WG.PresharedKey = psk
		}
		if u.WG.Address == "" {
			addr, err := AllocateWGAddress(wg.WireGuard.WGSubnet(), taken)
			if err != nil {
				return fmt.Errorf("user %s: %w", u.Name, err)
			}
			u.WG.Address = addr
			taken = append(taken, addr)
		}
	}
	return nil
}
