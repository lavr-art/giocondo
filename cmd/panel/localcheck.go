package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func agentIdentity(path string, verify bool) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("agent certificate: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", errors.New("invalid agent certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", errors.New("invalid agent certificate")
	}
	if verify {
		if _, err := tls.LoadX509KeyPair(path, filepath.Join(filepath.Dir(path), "agent.key")); err != nil {
			return "", errors.New("agent certificate and private key are invalid or do not match")
		}
		ca, err := os.ReadFile(filepath.Join(filepath.Dir(path), "ca.crt"))
		if err != nil {
			return "", fmt.Errorf("agent CA: %w", err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			return "", errors.New("invalid agent CA")
		}
		if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
			return "", errors.New("agent certificate is expired or not signed by its CA")
		}
	}
	if cert.Subject.CommonName == "" {
		return "", errors.New("agent certificate has no node name")
	}
	return cert.Subject.CommonName, nil
}

func inspectLocalNode(path, cert, config, name string, check bool, timeout time.Duration, out io.Writer) error {
	if timeout <= 0 {
		return errors.New("timeout must be positive")
	}
	started := time.Now().UTC().Truncate(time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if check {
		var err error
		name, err = agentIdentity(cert, true)
		if err != nil {
			return err
		}
	} else if _, err := os.Stat(cert); err == nil {
		name, err = agentIdentity(cert, false)
		if err != nil {
			return err
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	u := url.URL{Scheme: "file", Path: absolute}
	q := url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(1000)"}}
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	db.SetMaxOpenConns(1)
	if !check {
		rows, err := db.QueryContext(ctx, `SELECT i.type,i.port FROM inbounds i JOIN nodes n ON n.id=i.node_id WHERE n.name=? AND n.runtime='local' ORDER BY i.id`, name)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		var ports []string
		for rows.Next() {
			var kind string
			var port int
			if err := rows.Scan(&kind, &port); err != nil {
				return err
			}
			protocol := "udp"
			if kind == "vless-reality" {
				protocol = "tcp"
			}
			if port < 1 || port > 65535 {
				return errors.New("invalid inbound port")
			}
			ports = append(ports, fmt.Sprintf("%d/%s", port, protocol))
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(ports) == 0 {
			return errors.New("local node has no inbounds")
		}
		_, err = fmt.Fprintln(out, strings.Join(ports, " "))
		return err
	}
	var last error
	for {
		last = localNodeReady(ctx, db, name, config, started)
		if last == nil {
			_, err = fmt.Fprintf(out, "local node %s ready\n", name)
			return err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("local node readiness timed out: %w", last)
		case <-time.After(2 * time.Second):
		}
	}
}

func localNodeReady(ctx context.Context, db *sql.DB, name, config string, started time.Time) error {
	var runtime, status, desired, applied, applyErr string
	var seen sql.NullString
	err := db.QueryRowContext(ctx, `SELECT runtime,status,last_seen,desired_hash,applied_hash,apply_error FROM nodes WHERE name=?`, name).Scan(&runtime, &status, &seen, &desired, &applied, &applyErr)
	if err != nil {
		return fmt.Errorf("read local node: %w", err)
	}
	if runtime != "local" {
		return errors.New("agent node is not local")
	}
	if status != "online" {
		return errors.New("agent is not online")
	}
	t, err := time.Parse(time.RFC3339, seen.String)
	if !seen.Valid || err != nil || t.Before(started) {
		return errors.New("waiting for a fresh agent heartbeat")
	}
	if applyErr != "" {
		return errors.New("agent reports a configuration apply error; inspect its journal")
	}
	if desired == "" || desired != applied {
		return errors.New("agent has not applied the desired configuration")
	}
	data, err := os.ReadFile(config)
	if err != nil {
		return fmt.Errorf("read installed config: %w", err)
	}
	if !json.Valid(data) {
		return errors.New("installed config is invalid JSON")
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != desired {
		return errors.New("installed config does not match the desired configuration")
	}
	return nil
}
