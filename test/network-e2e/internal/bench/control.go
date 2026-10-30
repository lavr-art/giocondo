package bench

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

type fixtureRecord struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
	TTL     uint32 `json:"ttl"`
	Proxied bool   `json:"proxied"`
}

// Control serves the local Cloudflare fixture, repository files and webhook log.
type Control struct {
	mu             sync.Mutex
	root, upstream string
	records        []fixtureRecord
	files          http.Handler
	client         *http.Client
}

func NewControl(root, upstream string) (*Control, error) {
	c := &Control{root: root, upstream: strings.TrimRight(upstream, "/"), records: []fixtureRecord{}, files: http.FileServer(http.Dir(root)), client: &http.Client{Timeout: 25 * time.Second}}
	data, err := os.ReadFile(filepath.Join(root, "records.json"))
	if err == nil {
		if err := json.Unmarshal(data, &c.records); err != nil {
			return nil, fmt.Errorf("load DNS records: %w", err)
		}
		if c.records == nil {
			c.records = []fixtureRecord{}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return c, nil
}

func replyJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func (c *Control) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if path == "/events" {
		c.events(w, r)
		return
	}
	if path == "/health/challenge" && r.Method == http.MethodGet {
		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, c.upstream+r.URL.RequestURI(), nil)
		if err != nil {
			http.Error(w, "invalid challenge upstream", http.StatusBadGateway)
			return
		}
		res, err := c.client.Do(req)
		if err != nil {
			http.Error(w, "challenge upstream unavailable", http.StatusBadGateway)
			return
		}
		defer func() { _ = res.Body.Close() }()
		data, err := readBounded(res.Body, 1<<20)
		if err != nil {
			http.Error(w, "read challenge upstream failed", http.StatusBadGateway)
			return
		}
		w.WriteHeader(res.StatusCode)
		_, _ = w.Write(data)
		return
	}
	const recordsPath = "/zones/test/dns_records"
	if path != recordsPath && !strings.HasPrefix(path, recordsPath+"/") {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		c.files.ServeHTTP(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Bearer local-test-token" {
		replyJSON(w, 403, map[string]any{})
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if r.Method == http.MethodGet {
		name := r.URL.Query().Get("name")
		records := []fixtureRecord{}
		for _, record := range c.records {
			if name == "" || record.Name == name {
				records = append(records, record)
			}
		}
		replyJSON(w, 200, map[string]any{"success": true, "result": records})
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPatch {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var record fixtureRecord
	if err := decodeJSONBody(w, r, &record); err != nil {
		http.Error(w, "invalid JSON body", 400)
		return
	}
	updated := append([]fixtureRecord(nil), c.records...)
	if r.Method == http.MethodPost {
		record.ID = fmt.Sprintf("record-%d", len(updated)+1)
		updated = append(updated, record)
	} else {
		id := strings.TrimPrefix(path, recordsPath+"/")
		found := false
		for i, old := range updated {
			if old.ID == id {
				record.ID = id
				updated[i] = record
				found = true
				break
			}
		}
		if !found {
			replyJSON(w, 404, map[string]any{"success": false})
			return
		}
	}
	data, err := json.Marshal(updated)
	if err == nil {
		err = writeAtomic(filepath.Join(c.root, "records.json"), data, 0644)
	}
	if err != nil {
		http.Error(w, "persist DNS records failed", 500)
		return
	}
	c.records = updated
	replyJSON(w, 200, map[string]any{"success": true, "result": record})
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, out any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("extra data after JSON body")
	}
	return nil
}

func (c *Control) events(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	path := filepath.Join(c.root, "events.jsonl")
	switch r.Method {
	case http.MethodGet:
		file, err := os.Open(path)
		if errors.Is(err, os.ErrNotExist) {
			replyJSON(w, 200, []any{})
			return
		}
		if err != nil {
			http.Error(w, "open event log failed", 500)
			return
		}
		defer func() { _ = file.Close() }()
		events := []json.RawMessage{}
		scanner := bufio.NewScanner(file)
		scanner.Buffer(make([]byte, 4096), 1<<20)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			if !json.Valid(line) {
				http.Error(w, "invalid event log", 500)
				return
			}
			events = append(events, json.RawMessage(line))
		}
		if err := scanner.Err(); err != nil {
			http.Error(w, "read event log failed", 500)
			return
		}
		replyJSON(w, 200, events)
	case http.MethodPost:
		var data json.RawMessage
		if err := decodeJSONBody(w, r, &data); err != nil {
			http.Error(w, "invalid JSON body", 400)
			return
		}
		// Compact webhook JSON so each persisted event occupies exactly one line.
		data, err := json.Marshal(data)
		if err != nil {
			http.Error(w, "invalid JSON body", 400)
			return
		}
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0644)
		if err != nil {
			http.Error(w, "open event log failed", 500)
			return
		}
		_, err = file.Write(append(data, '\n'))
		closeErr := file.Close()
		if err != nil || closeErr != nil {
			http.Error(w, "write event log failed", 500)
			return
		}
		replyJSON(w, 200, map[string]any{})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (c *Control) AnswerDNS(raw []byte) ([]byte, error) {
	var request dnsmessage.Message
	if err := request.Unpack(raw); err != nil {
		return nil, fmt.Errorf("parse DNS query: %w", err)
	}
	if request.Response || request.OpCode != 0 || len(request.Questions) != 1 {
		return nil, errors.New("DNS query must contain exactly one question")
	}
	question := request.Questions[0]
	response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, Authoritative: true, RecursionDesired: request.RecursionDesired, RecursionAvailable: true}, Questions: request.Questions}
	if question.Class != dnsmessage.ClassINET {
		response.RCode = dnsmessage.RCodeNotImplemented
		return response.Pack()
	}
	name := strings.TrimSuffix(question.Name.String(), ".")
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, record := range c.records {
		if !strings.EqualFold(record.Name, name) {
			continue
		}
		ip, err := netip.ParseAddr(record.Content)
		if err != nil {
			continue
		}
		resource := dnsmessage.Resource{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: question.Type, Class: dnsmessage.ClassINET, TTL: record.TTL}}
		switch {
		case question.Type == dnsmessage.TypeA && record.Type == "A" && ip.Is4():
			resource.Body = &dnsmessage.AResource{A: ip.As4()}
		case question.Type == dnsmessage.TypeAAAA && record.Type == "AAAA" && ip.Is6():
			resource.Body = &dnsmessage.AAAAResource{AAAA: ip.As16()}
		default:
			continue
		}
		response.Answers = append(response.Answers, resource)
	}
	return response.Pack()
}

func (c *Control) serveDNSUDP(ctx context.Context, conn net.PacketConn) error {
	buf := make([]byte, 65535)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			return err
		}
		n, addr, err := conn.ReadFrom(buf)
		if err != nil {
			if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
				continue
			}
			return err
		}
		answer, err := c.AnswerDNS(buf[:n])
		if err != nil {
			continue
		}
		if err := conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			return err
		}
		if _, err := conn.WriteTo(answer, addr); err != nil {
			return err
		}
	}
}

func (c *Control) serveDNSTCP(ctx context.Context, listener net.Listener) error {
	var clients sync.WaitGroup
	defer clients.Wait()
	for {
		conn, err := listener.Accept()
		if err != nil {
			return err
		}
		clients.Add(1)
		go func() {
			defer clients.Done()
			defer func() { _ = conn.Close() }()
			for ctx.Err() == nil {
				if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
					return
				}
				var length [2]byte
				if _, err := io.ReadFull(conn, length[:]); err != nil {
					return
				}
				raw := make([]byte, int(binary.BigEndian.Uint16(length[:])))
				if _, err := io.ReadFull(conn, raw); err != nil {
					return
				}
				answer, err := c.AnswerDNS(raw)
				if err != nil {
					return
				}
				binary.BigEndian.PutUint16(length[:], uint16(len(answer)))
				if _, err := io.Copy(conn, strings.NewReader(string(append(length[:], answer...)))); err != nil {
					return
				}
			}
		}()
	}
}

func RunControl(ctx context.Context, root string) error {
	fixture, err := NewControl(root, "http://panel:8081")
	if err != nil {
		return err
	}
	cert, err := tls.LoadX509KeyPair(filepath.Join(root, "pki/server.crt"), filepath.Join(root, "pki/server.key"))
	if err != nil {
		return fmt.Errorf("load control TLS certificate: %w", err)
	}
	config := net.ListenConfig{}
	udp, err := config.ListenPacket(ctx, "udp", ":53")
	if err != nil {
		return err
	}
	defer func() { _ = udp.Close() }()
	tcp, err := config.Listen(ctx, "tcp", ":53")
	if err != nil {
		return err
	}
	defer func() { _ = tcp.Close() }()
	plain, err := config.Listen(ctx, "tcp", ":8000")
	if err != nil {
		return err
	}
	defer func() { _ = plain.Close() }()
	secure, err := config.Listen(ctx, "tcp", ":8443")
	if err != nil {
		return err
	}
	defer func() { _ = secure.Close() }()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	httpServer := &http.Server{Handler: fixture, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	tlsServer := &http.Server{Handler: fixture, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 30 * time.Second}
	errorsOut := make(chan error, 4)
	go func() { errorsOut <- fixture.serveDNSUDP(ctx, udp) }()
	go func() { errorsOut <- fixture.serveDNSTCP(ctx, tcp) }()
	go func() { errorsOut <- httpServer.Serve(plain) }()
	go func() {
		errorsOut <- tlsServer.Serve(tls.NewListener(secure, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}))
	}()
	select {
	case err = <-errorsOut:
	case <-ctx.Done():
		err = ctx.Err()
	}
	cancel()
	_ = udp.Close()
	_ = tcp.Close()
	shutdown, cancelShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelShutdown()
	_ = httpServer.Shutdown(shutdown)
	_ = tlsServer.Shutdown(shutdown)
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
