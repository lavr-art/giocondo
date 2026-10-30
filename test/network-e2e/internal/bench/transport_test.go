package bench

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestDNSUDPAndFragmentedTCPQueries(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "records.json"), []byte(`[{"id":"record-1","name":"node.test","type":"A","content":"192.0.2.9","ttl":60}]`), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, err := NewControl(root, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	config := net.ListenConfig{}
	udp, err := config.ListenPacket(ctx, "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = udp.Close() }()
	tcp, err := config.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tcp.Close() }()
	finished := make(chan error, 2)
	go func() { finished <- fixture.serveDNSUDP(ctx, udp) }()
	go func() { finished <- fixture.serveDNSTCP(ctx, tcp) }()
	query := dnsmessage.Message{Header: dnsmessage.Header{ID: 120}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("node.test."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	raw, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ network, address string }{{"udp", udp.LocalAddr().String()}, {"tcp", tcp.Addr().String()}} {
		t.Run(tc.network, func(t *testing.T) {
			dialer := net.Dialer{Timeout: time.Second}
			conn, err := dialer.DialContext(ctx, tc.network, tc.address)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = conn.Close() }()
			if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if tc.network == "udp" {
				if _, err := conn.Write(raw); err != nil {
					t.Fatal(err)
				}
			} else {
				packet := binary.BigEndian.AppendUint16(nil, uint16(len(raw)))
				packet = append(packet, raw...)
				// TCP may split both the length prefix and the question at any byte.
				for _, b := range packet {
					if _, err := conn.Write([]byte{b}); err != nil {
						t.Fatal(err)
					}
				}
			}
			var response []byte
			if tc.network == "udp" {
				response = make([]byte, 4096)
				n, err := conn.Read(response)
				if err != nil {
					t.Fatal(err)
				}
				response = response[:n]
			} else {
				var length [2]byte
				if _, err := io.ReadFull(conn, length[:]); err != nil {
					t.Fatal(err)
				}
				response = make([]byte, binary.BigEndian.Uint16(length[:]))
				if _, err := io.ReadFull(conn, response); err != nil {
					t.Fatal(err)
				}
			}
			var message dnsmessage.Message
			if err := message.Unpack(response); err != nil {
				t.Fatal(err)
			}
			if message.ID != 120 || len(message.Answers) != 1 {
				t.Fatalf("DNS response = %+v", message)
			}
			address, ok := message.Answers[0].Body.(*dnsmessage.AResource)
			if !ok || address.A != [4]byte{192, 0, 2, 9} {
				t.Fatalf("DNS answer = %+v", message.Answers[0])
			}
		})
	}
	cancel()
	_ = tcp.Close()
	_ = udp.Close()
	for range 2 {
		select {
		case err := <-finished:
			if !errors.Is(err, net.ErrClosed) && !errors.Is(err, context.Canceled) {
				t.Fatalf("DNS shutdown: %v", err)
			}
		case <-time.After(6 * time.Second):
			t.Fatal("DNS server did not stop")
		}
	}
}

func TestControlConcurrentDNSAndAPIUpdates(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "records.json"), []byte(`[{"id":"record-1","name":"node.test","type":"A","content":"192.0.2.9","ttl":60}]`), 0600); err != nil {
		t.Fatal(err)
	}
	fixture, err := NewControl(root, "")
	if err != nil {
		t.Fatal(err)
	}
	query := dnsmessage.Message{Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("node.test."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}}
	raw, err := query.Pack()
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for worker := range 4 {
		workers.Go(func() {
			for iteration := range 10 {
				body := fmt.Sprintf(`{"name":"node.test","type":"A","content":"192.0.2.%d","ttl":%d}`, worker+1, iteration+60)
				req := httptest.NewRequest("PATCH", "/zones/test/dns_records/record-1", strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer local-test-token")
				w := httptest.NewRecorder()
				fixture.ServeHTTP(w, req)
				if w.Code != 200 {
					t.Errorf("update: %d %s", w.Code, w.Body.String())
					return
				}
				response, err := fixture.AnswerDNS(raw)
				if err != nil {
					t.Error(err)
					return
				}
				var message dnsmessage.Message
				if err := message.Unpack(response); err != nil || len(message.Answers) != 1 {
					t.Errorf("concurrent DNS answer: %+v %v", message, err)
					return
				}
			}
		})
	}
	workers.Wait()
	if _, err := NewControl(root, ""); err != nil {
		t.Fatalf("concurrent updates corrupted persisted state: %v", err)
	}
}

func TestHTTPRejectsUntrustedTLSAndCancelledRequests(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("should not reach client")) }))
	defer server.Close()
	var out bytes.Buffer
	err := HTTPRequest(context.Background(), http.DefaultClient, "GET", server.URL, "", "", &out)
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("untrusted TLS error = %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("untrusted response written: %q", out.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err = HTTPRequest(ctx, server.Client(), "GET", server.URL, "", "", &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled request error = %v", err)
	}
	_, err = readBounded(strings.NewReader("oversized"), 4)
	if err == nil || err.Error() != "response exceeds size limit" {
		t.Fatalf("bounded read error = %v", err)
	}
}
