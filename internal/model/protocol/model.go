// Package protocol defines VPN protocol parameters independently of engine rendering.
package protocol

import (
	"fmt"
	"strings"
)

// InboundType is the inbound protocol on a node.
type InboundType string

// Supported protocols.
const (
	TypeVLESSReality InboundType = "vless-reality"
	TypeHysteria2    InboundType = "hysteria2"
	TypeWireGuard    InboundType = "wireguard"
)

// TLSMode is the certificate source for Hysteria2.
type TLSMode string

// Hysteria2 TLS modes.
const (
	TLSSelfSigned TLSMode = "self-signed"
	TLSACME       TLSMode = "acme"
)

// DefaultWGSubnet is the default WireGuard subnet. The server gets .1, clients start at .2.
const DefaultWGSubnet = "10.70.0.0/24"

// monitorUserPrefix starts the name of a node's monitor user, which the availability probes connect as.
const monitorUserPrefix = "giocondo-vpn-probe-"

// MonitorUserName is the name of the monitor user of a node.
func MonitorUserName(nodeID int64) string { return fmt.Sprintf("%s%d", monitorUserPrefix, nodeID) }

// IsMonitorUser reports whether name belongs to a monitor user. Such users are served but not counted
// in traffic statistics, so the names are reserved for them.
func IsMonitorUser(name string) bool { return strings.HasPrefix(name, monitorUserPrefix) }

// Node is a node and its inbound profiles.
type Node struct {
	ID       string    `yaml:"id" json:"id"`
	Host     string    `yaml:"host" json:"host"` // domain or IP clients connect to
	Inbounds []Inbound `yaml:"inbounds" json:"inbounds"`
}

// Inbound is one protocol on a node. Exactly one parameter block matching Type is set.
type Inbound struct {
	Type      InboundType      `yaml:"type" json:"type"`
	Tag       string           `yaml:"tag,omitempty" json:"tag,omitempty"`
	Port      uint16           `yaml:"port" json:"port"`
	Reality   *RealityParams   `yaml:"reality,omitempty" json:"reality,omitempty"`
	Hysteria2 *Hysteria2Params `yaml:"hysteria2,omitempty" json:"hysteria2,omitempty"`
	WireGuard *WireGuardParams `yaml:"wireguard,omitempty" json:"wireguard,omitempty"`
}

// RealityParams holds VLESS Reality parameters.
type RealityParams struct {
	PrivateKey string   `yaml:"private_key" json:"private_key"`
	ShortIDs   []string `yaml:"short_ids" json:"short_ids"`
	Dest       string   `yaml:"dest" json:"dest"`               // handshake site, host:port
	ServerName string   `yaml:"server_name" json:"server_name"` // SNI sent by the client
}

// Hysteria2Params holds Hysteria2 parameters. The user password is their UUID.
type Hysteria2Params struct {
	TLS          TLSMode `yaml:"tls,omitempty" json:"tls,omitempty"` // self-signed by default
	CertPEM      string  `yaml:"cert_pem,omitempty" json:"cert_pem,omitempty"`
	KeyPEM       string  `yaml:"key_pem,omitempty" json:"key_pem,omitempty"`
	ACMEEmail    string  `yaml:"acme_email,omitempty" json:"acme_email,omitempty"`
	ObfsPassword string  `yaml:"obfs_password,omitempty" json:"obfs_password,omitempty"` // salamander when set
	UpMbps       int     `yaml:"up_mbps,omitempty" json:"up_mbps,omitempty"`
	DownMbps     int     `yaml:"down_mbps,omitempty" json:"down_mbps,omitempty"`
}

// WireGuardParams is the WireGuard server side.
type WireGuardParams struct {
	PrivateKey string `yaml:"private_key" json:"private_key"`
	Subnet     string `yaml:"subnet,omitempty" json:"subnet,omitempty"` // DefaultWGSubnet by default
	MTU        int    `yaml:"mtu,omitempty" json:"mtu,omitempty"`       // 1408 by default
}

// User is a VPN user.
type User struct {
	Name string  `yaml:"name" json:"name"`
	UUID string  `yaml:"uuid" json:"uuid"`
	WG   *WGPeer `yaml:"wg,omitempty" json:"wg,omitempty"`
}

// WGPeer holds a user's WireGuard keys and address.
type WGPeer struct {
	PrivateKey   string `yaml:"private_key" json:"private_key"`
	PublicKey    string `yaml:"public_key" json:"public_key"`
	PresharedKey string `yaml:"preshared_key" json:"preshared_key"`
	Address      string `yaml:"address" json:"address"` // 10.70.0.N/32
}

// EffectiveTag is the inbound tag in the sing-box config: the given one or "<type>-<port>".
func (in Inbound) EffectiveTag() string {
	if in.Tag != "" {
		return in.Tag
	}

	return fmt.Sprintf("%s-%d", in.Type, in.Port)
}

// WGSubnet returns the WireGuard subnet, taking the default into account.
func (p *WireGuardParams) WGSubnet() string {
	if p.Subnet == "" {
		return DefaultWGSubnet
	}

	return p.Subnet
}

// TLSMode returns the TLS mode, taking the default into account.
func (p *Hysteria2Params) TLSMode() TLSMode {
	if p.TLS == "" {
		return TLSSelfSigned
	}

	return p.TLS
}

// WireGuardInbound returns the node WireGuard inbound or nil.
func (n *Node) WireGuardInbound() *Inbound {
	for i := range n.Inbounds {
		if n.Inbounds[i].Type == TypeWireGuard {
			return &n.Inbounds[i]
		}
	}

	return nil
}
