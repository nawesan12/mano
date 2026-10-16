// Package arca emite facturas electrónicas con los web services de ARCA (ex AFIP): WSAA para
// autenticarse con el certificado de la CUIT y WSFEv1 para pedir el CAE. Solo librería estándar.
package arca

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"sort"
	"time"
)

// OIDs de CMS (RFC 5652).
var (
	oidData          = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidSignedData    = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidContentType   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 3}
	oidMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}
	oidSigningTime   = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 5}
	oidSHA256        = asn1.ObjectIdentifier{2, 16, 840, 1, 101, 3, 4, 2, 1}
	oidRSA           = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 1, 1}
)

type issuerAndSerial struct {
	Issuer asn1.RawValue
	Serial *big.Int
}

type signerInfo struct {
	Version            int
	SID                issuerAndSerial
	DigestAlgorithm    pkix.AlgorithmIdentifier
	SignedAttrs        asn1.RawValue // [0] IMPLICIT SET OF Attribute
	SignatureAlgorithm pkix.AlgorithmIdentifier
	Signature          []byte
}

type encapContent struct {
	Type    asn1.ObjectIdentifier
	Content asn1.RawValue // [0] EXPLICIT OCTET STRING
}

type signedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue // SET OF AlgorithmIdentifier
	Encap            encapContent
	Certificates     asn1.RawValue // [0] IMPLICIT SET OF Certificate
	SignerInfos      asn1.RawValue // SET OF SignerInfo
}

type contentInfo struct {
	Type    asn1.ObjectIdentifier
	Content asn1.RawValue // [0] EXPLICIT SignedData
}

type attribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue // SET OF valor
}

func mustMarshal(v any) []byte {
	b, err := asn1.Marshal(v)
	if err != nil {
		panic(err) // solo estructuras fijas de este archivo
	}
	return b
}

// set arma un SET OF en DER: los elementos van ordenados por su codificación.
func set(elems ...[]byte) []byte {
	sort.Slice(elems, func(i, j int) bool { return bytes.Compare(elems[i], elems[j]) < 0 })
	return mustMarshal(asn1.RawValue{Class: asn1.ClassUniversal, Tag: asn1.TagSet, IsCompound: true, Bytes: bytes.Join(elems, nil)})
}

func attr(oid asn1.ObjectIdentifier, value any) []byte {
	v := mustMarshal(value)
	return mustMarshal(attribute{Type: oid, Values: asn1.RawValue{FullBytes: set(v)}})
}

// SignCMS firma content con la clave y el certificado (CMS SignedData con el contenido adentro, SHA-256 + RSA),
// que es lo que pide WSAA para el ticket de acceso.
func SignCMS(content []byte, cert *x509.Certificate, key *rsa.PrivateKey, now time.Time) ([]byte, error) {
	if cert == nil || key == nil {
		return nil, errors.New("falta el certificado o la clave")
	}
	digest := sha256.Sum256(content)
	attrs := [][]byte{
		attr(oidContentType, oidData),
		attr(oidSigningTime, now.UTC()),
		attr(oidMessageDigest, digest[:]),
	}
	// se firma la codificación DER del SET de atributos (con tag SET, no el [0] implícito)
	signedSet := set(attrs...)
	h := sha256.Sum256(signedSet)
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, h[:])
	if err != nil {
		return nil, err
	}
	implicit := append([]byte(nil), signedSet...)
	implicit[0] = 0xA0 // [0] IMPLICIT: mismo contenido, otro tag

	sha := pkix.AlgorithmIdentifier{Algorithm: oidSHA256, Parameters: asn1.NullRawValue}
	si := signerInfo{
		Version:            1,
		SID:                issuerAndSerial{Issuer: asn1.RawValue{FullBytes: cert.RawIssuer}, Serial: cert.SerialNumber},
		DigestAlgorithm:    sha,
		SignedAttrs:        asn1.RawValue{FullBytes: implicit},
		SignatureAlgorithm: pkix.AlgorithmIdentifier{Algorithm: oidRSA, Parameters: asn1.NullRawValue},
		Signature:          sig,
	}
	octets := mustMarshal(content)
	sd := signedData{
		Version:          1,
		DigestAlgorithms: asn1.RawValue{FullBytes: set(mustMarshal(sha))},
		Encap:            encapContent{Type: oidData, Content: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: octets}},
		Certificates:     asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: cert.Raw},
		SignerInfos:      asn1.RawValue{FullBytes: set(mustMarshal(si))},
	}
	return asn1.Marshal(contentInfo{Type: oidSignedData, Content: asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: mustMarshal(sd)}})
}
