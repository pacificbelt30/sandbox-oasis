// Package workertransport provides short-lived, token-bound mutual TLS for the
// worker-to-auth-proxy hop. It contains no upstream/provider credentials.
package workertransport

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"net/url"
	"time"
)

const LocalURL = "http://127.0.0.1:18083"
const ServerName = "oaiso-auth-proxy"
const ConfigPath = "/run/codex-dock-control/transport.json"
const MaxTTL = 3600

type Config struct {
	Endpoint    string `json:"endpoint"`
	CA          []byte `json:"ca"`
	Certificate []byte `json:"certificate"`
	Key         []byte `json:"key"`
}
type Authority struct {
	ca     *x509.Certificate
	key    *ecdsa.PrivateKey
	PEM    []byte
	Server tls.Certificate
}

func Binding(token string) string { h := sha256.Sum256([]byte(token)); return hex.EncodeToString(h[:]) }
func serial() (*big.Int, error)   { return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128)) }
func certificate(template, parent *x509.Certificate, key, signer *ecdsa.PrivateKey) (tls.Certificate, []byte, []byte, error) {
	der, e := x509.CreateCertificate(rand.Reader, template, parent, &key.PublicKey, signer)
	if e != nil {
		return tls.Certificate{}, nil, nil, e
	}
	secret, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		return tls.Certificate{}, nil, nil, e
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: secret})
	pair, e := tls.X509KeyPair(certPEM, keyPEM)
	return pair, certPEM, keyPEM, e
}
func NewAuthority() (*Authority, error) {
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	n, e := serial()
	if e != nil {
		return nil, e
	}
	now := time.Now()
	ca := &x509.Certificate{SerialNumber: n, Subject: pkix.Name{CommonName: "Oaiso ephemeral worker transport"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(10 * 365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	_, public, _, e := certificate(ca, ca, key, key)
	if e != nil {
		return nil, e
	}
	block, _ := pem.Decode(public)
	ca, e = x509.ParseCertificate(block.Bytes)
	if e != nil {
		return nil, e
	}
	serverKey, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return nil, e
	}
	n, e = serial()
	if e != nil {
		return nil, e
	}
	server := &x509.Certificate{SerialNumber: n, Subject: pkix.Name{CommonName: ServerName}, DNSNames: []string{ServerName}, NotBefore: now.Add(-time.Minute), NotAfter: ca.NotAfter, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	pair, _, _, e := certificate(server, ca, serverKey, key)
	if e != nil {
		return nil, e
	}
	return &Authority{ca: ca, key: key, PEM: public, Server: pair}, nil
}
func (a *Authority) TLSConfig() *tls.Config {
	pool := x509.NewCertPool()
	pool.AddCert(a.ca)
	return &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{a.Server}, ClientAuth: tls.RequireAndVerifyClientCert, ClientCAs: pool}
}
func (a *Authority) Issue(token string, ttl int) (Config, error) {
	if len(token) != 68 || token[:4] != "cdx-" || ttl < 1 || ttl > MaxTTL {
		return Config{}, errors.New("invalid worker transport lease")
	}
	if _, e := hex.DecodeString(token[4:]); e != nil {
		return Config{}, errors.New("invalid worker transport token")
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return Config{}, e
	}
	n, e := serial()
	if e != nil {
		return Config{}, e
	}
	now := time.Now()
	expiry := now.Add(time.Duration(ttl) * time.Second)
	if expiry.After(a.ca.NotAfter) {
		return Config{}, errors.New("proxy transport authority expires; restart proxy")
	}
	template := &x509.Certificate{SerialNumber: n, Subject: pkix.Name{CommonName: Binding(token)}, NotBefore: now.Add(-time.Minute), NotAfter: expiry, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	_, certPEM, keyPEM, e := certificate(template, a.ca, key, a.key)
	return Config{CA: append([]byte(nil), a.PEM...), Certificate: certPEM, Key: keyPEM}, e
}
func (c Config) TLSConfig() (*tls.Config, error) {
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("invalid worker TLS endpoint")
	}
	if len(c.CA) > 16<<10 || len(c.Certificate) > 16<<10 || len(c.Key) > 16<<10 {
		return nil, errors.New("oversized worker transport credentials")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(c.CA) {
		return nil, errors.New("invalid worker transport authority")
	}
	pair, e := tls.X509KeyPair(c.Certificate, c.Key)
	if e != nil {
		return nil, errors.New("invalid worker transport certificate")
	}
	return &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: pool, ServerName: ServerName, Certificates: []tls.Certificate{pair}}, nil
}
func Bound(state *tls.ConnectionState, token string) bool {
	return state != nil && len(state.VerifiedChains) > 0 && len(state.VerifiedChains[0]) > 0 && state.VerifiedChains[0][0].Subject.CommonName == Binding(token)
}
