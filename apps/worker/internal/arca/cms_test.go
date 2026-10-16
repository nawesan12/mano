package arca

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCert(t *testing.T) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()
	key, _ := rsa.GenerateKey(rand.Reader, 2048)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(4242), Subject: pkix.Name{CommonName: "mano-test", SerialNumber: "CUIT 20111111112"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return cert, key
}

// La firma tiene que ser un CMS que openssl valide y del que saque el contenido intacto (es lo que hace WSAA).
func TestSignCMSVerifiesWithOpenSSL(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("sin openssl")
	}
	cert, key := testCert(t)
	tra := []byte(`<?xml version="1.0" encoding="UTF-8"?><loginTicketRequest version="1.0"><service>wsfe</service></loginTicketRequest>`)
	cms, err := SignCMS(tra, cert, key, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	in := filepath.Join(dir, "tra.cms")
	out := filepath.Join(dir, "tra.xml")
	_ = os.WriteFile(in, cms, 0o600)
	cmd := exec.Command("openssl", "cms", "-verify", "-inform", "DER", "-in", in, "-noverify", "-out", out)
	if b, err := cmd.CombinedOutput(); err != nil || !strings.Contains(string(b), "Verification successful") {
		t.Fatalf("openssl no validó la firma: %v\n%s", err, b)
	}
	got, _ := os.ReadFile(out)
	if string(got) != string(tra) {
		t.Fatalf("contenido: %q", got)
	}
}
