package service

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"strings"

	"github.com/samur005/3s-ui/util/common"

	"github.com/gin-gonic/gin"
)

// Node side of optional mutual TLS. With apiClientCA set (PEM, one or more CA
// certificates) the panel asks TLS clients for a certificate and apiv2 only
// answers requests whose certificate chains to one of those CAs -- on top of
// the API token, not instead of it. The panel's own UI (api/, cookies) is
// unaffected. It needs the panel to terminate TLS itself (certificate files
// or ACME); behind nginx the certificate never reaches the panel, so apiv2
// then refuses everything while the setting is on.

func init() { defaultValueMap["apiClientCA"] = "" }

// ParseClientCAs parses the apiClientCA setting; nil pool for an empty value.
func ParseClientCAs(raw string) (*x509.CertPool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	pool := x509.NewCertPool()
	rest := []byte(raw)
	n := 0
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, common.NewErrorf("apiClientCA: %v", err)
		}
		pool.AddCert(cert)
		n++
	}
	if n == 0 {
		return nil, common.NewError("apiClientCA: no PEM certificate found")
	}
	return pool, nil
}

func (s *SettingService) GetAPIClientCAs() (*x509.CertPool, error) {
	raw, err := s.getString("apiClientCA")
	if err != nil {
		return nil, err
	}
	return ParseClientCAs(raw)
}

// RequireClientCert is the apiv2 middleware: a request must have arrived over
// TLS with a client certificate the handshake verified against the pool.
func RequireClientCert() gin.HandlerFunc {
	return func(c *gin.Context) {
		st := c.Request.TLS
		if st == nil || len(st.VerifiedChains) == 0 {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"success": false, "msg": "client certificate required (mTLS)"})
			return
		}
		c.Next()
	}
}
