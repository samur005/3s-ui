package service

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"
)

type RealityScanResult struct {
	OK           bool   `json:"ok"`
	SNI          string `json:"sni"`
	TLSVersion   string `json:"tlsVersion"`
	ALPN         string `json:"alpn"`
	Cipher       string `json:"cipher"`
	CertSubject  string `json:"certSubject"`
	CertIssuer   string `json:"certIssuer"`
	CertExpiry   string `json:"certExpiry"`
	ChainBytes   int    `json:"chainBytes"`
	LatencyMs    int64  `json:"latencyMs"`
	Error        string `json:"error,omitempty"`
}

func ScanRealityTarget(domain string) *RealityScanResult {
	start := time.Now()
	res := &RealityScanResult{SNI: domain}

	conn, err := net.DialTimeout("tcp", domain+":443", 8*time.Second)
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer conn.Close()

	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         domain,
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1"},
	})
	if err := tlsConn.Handshake(); err != nil {
		res.Error = err.Error()
		return res
	}

	state := tlsConn.ConnectionState()
	res.OK = true
	res.TLSVersion = tlsVersionString(state.Version)
	res.ALPN = state.NegotiatedProtocol
	res.Cipher = tls.CipherSuiteName(state.CipherSuite)
	res.LatencyMs = time.Since(start).Milliseconds()

	if len(state.PeerCertificates) > 0 {
		cert := state.PeerCertificates[0]
		res.CertSubject = cert.Subject.CommonName
		if len(cert.Issuer.Organization) > 0 {
			res.CertIssuer = cert.Issuer.Organization[0]
		}
		res.CertExpiry = cert.NotAfter.Format("2006-01-02 15:04")
		for _, c := range state.PeerCertificates {
			res.ChainBytes += len(c.Raw)
		}
	}
	return res
}

func tlsVersionString(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "1.3"
	case tls.VersionTLS12:
		return "1.2"
	default:
		return fmt.Sprintf("0x%x", v)
	}
}
