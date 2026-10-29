package subscription

import (
	"encoding/base64"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/artemlavr/giocondo/internal/singbox"
)

// Links returns vless://, hysteria2:// and wireguard:// links, one per server.
// Parameter names are compatible with v2rayN/v2rayNG and ray2sing (the Hiddify converter).
func Links(p Profile) ([]string, error) {
	srvs, err := servers(p)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(srvs))
	for _, s := range srvs {
		out = append(out, link(s))
	}
	return out, nil
}

// LinksBase64 is the subscription body in base64 format: links separated by \n, encoded as a whole.
func LinksBase64(p Profile) ([]byte, error) {
	links, err := Links(p)
	if err != nil {
		return nil, err
	}
	return []byte(base64.StdEncoding.EncodeToString([]byte(strings.Join(links, "\n")))), nil
}

func link(s server) string {
	hostport := net.JoinHostPort(s.Host, strconv.Itoa(int(s.Port)))
	u := url.URL{Host: hostport, Fragment: s.Name}
	q := url.Values{}
	switch s.Type {
	case singbox.TypeVLESSReality:
		u.Scheme = "vless"
		u.User = url.User(s.UUID)
		q.Set("encryption", "none")
		q.Set("type", "tcp")
		q.Set("security", "reality")
		q.Set("pbk", s.PublicKey)
		q.Set("sid", s.ShortID)
		q.Set("sni", s.SNI)
		q.Set("fp", "chrome")
		q.Set("flow", "xtls-rprx-vision")
	case singbox.TypeHysteria2:
		u.Scheme = "hysteria2"
		u.User = url.User(s.UUID)
		q.Set("sni", s.SNI)
		if s.Insecure {
			q.Set("insecure", "1")
			q.Set("pinSHA256", s.PinSHA256)
		}
		if s.ObfsPassword != "" {
			q.Set("obfs", "salamander")
			q.Set("obfs-password", s.ObfsPassword)
		}
	case singbox.TypeWireGuard:
		u.Scheme = "wireguard"
		u.User = url.User(s.WGPrivateKey)
		q.Set("publickey", s.WGPeerPubKey)
		q.Set("presharedkey", s.WGPresharedKey)
		q.Set("address", s.WGAddress)
		q.Set("mtu", strconv.Itoa(s.MTU))
	}
	u.RawQuery = q.Encode()
	return u.String()
}
