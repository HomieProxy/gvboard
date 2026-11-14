package node

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/pem"
	"math/big"
	"time"
)

// x25519Pair 生成 X25519 密钥对，返回 URL 安全、无填充的 base64（对应原项目 sodium crypto_box_keypair）。
func x25519Pair() (priv, pub string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(k.Bytes()), base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

// x25519Public 由已有私钥推导公钥，私钥格式不正确时返回 false。
func x25519Public(priv string) (string, bool) {
	b, err := base64.RawURLEncoding.DecodeString(priv)
	if err != nil {
		return "", false
	}
	k, err := ecdh.X25519().NewPrivateKey(b)
	if err != nil {
		return "", false
	}
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), true
}

// echKeyPair 生成 ECH 密钥（对应原项目 Helper::generateEchKeyPair），outerSNI 为外层伪装域名。
// 返回服务端使用的 ECHKeys 与客户端使用的 ECHConfigList，均为标准 base64。
func echKeyPair(outerSNI string) (key, config string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	var id [1]byte
	if _, err := rand.Read(id[:]); err != nil {
		return "", "", err
	}
	u16 := func(b *bytes.Buffer, v int) { _ = binary.Write(b, binary.BigEndian, uint16(v)) }

	var data bytes.Buffer
	data.WriteByte(id[0])
	u16(&data, 0x0020) // KEM：DHKEM(X25519, HKDF-SHA256)
	pub := k.PublicKey().Bytes()
	u16(&data, len(pub))
	data.Write(pub)
	suites := []int{0x0001, 0x0001, 0x0001, 0x0002, 0x0001, 0x0003}
	u16(&data, len(suites)*2)
	for _, s := range suites {
		u16(&data, s)
	}
	data.WriteByte(0) // maximum_name_length
	data.WriteByte(byte(len(outerSNI)))
	data.WriteString(outerSNI)
	u16(&data, 0) // extensions

	var cfg bytes.Buffer
	u16(&cfg, 0xfe0d)
	u16(&cfg, data.Len())
	cfg.Write(data.Bytes())

	var keys bytes.Buffer
	u16(&keys, cfg.Len())
	keys.Write(cfg.Bytes())
	u16(&keys, 1)
	keys.WriteByte(id[0])
	priv := k.Bytes()
	u16(&keys, len(priv))
	keys.Write(priv)

	return base64.StdEncoding.EncodeToString(keys.Bytes()), base64.StdEncoding.EncodeToString(cfg.Bytes()), nil
}

// selfSignedCert 生成 P-256 自签名证书（有效期 3650 天），返回 PEM 证书、PKCS#8 PEM 私钥
// 与证书 DER 的 SHA-256 十六进制指纹（客户端证书固定使用）。
func selfSignedCert(commonName string, now time.Time) (certPEM, keyPEM, pin string, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return "", "", "", err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    now,
		NotAfter:     now.AddDate(0, 0, 3650),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", "", err
	}
	pk, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return "", "", "", err
	}
	sum := sha256.Sum256(der)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pk})),
		hex.EncodeToString(sum[:]), nil
}
