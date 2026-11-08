package gloryofkings

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
)

// parsePublicKey 把营地的 base64 公钥补成 PEM 并解析。
func parsePublicKey(encoded string) (*rsa.PublicKey, error) {
	if encoded == "" {
		return nil, errors.New("缺少公钥")
	}
	body := strings.Join(strings.Fields(encoded), "")
	der, err := base64.StdEncoding.DecodeString(body)
	if err != nil {
		return nil, err
	}
	key, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		pemText := "-----BEGIN PUBLIC KEY-----\n" + wrapBase64(body, 64) + "\n-----END PUBLIC KEY-----\n"
		block, _ := pem.Decode([]byte(pemText))
		if block == nil {
			return nil, err
		}
		parsed, parseErr := x509.ParsePKIXPublicKey(block.Bytes)
		if parseErr != nil {
			return nil, err
		}
		key = parsed
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, errors.New("公钥不是 RSA 公钥")
	}
	return pub, nil
}

func wrapBase64(value string, width int) string {
	var builder strings.Builder
	for i := 0; i < len(value); i += width {
		end := i + width
		if end > len(value) {
			end = len(value)
		}
		builder.WriteString(value[i:end])
		builder.WriteByte('\n')
	}
	return builder.String()
}

// rsaPublicEncrypt 用公钥分段加密（PKCS#1 v1.5），每段最多 密钥长度-11 字节。
// 与 Node 的 crypto.publicEncrypt 行为一致。
func rsaPublicEncrypt(encoded string, plain []byte) ([]byte, error) {
	pub, err := parsePublicKey(encoded)
	if err != nil {
		return nil, err
	}
	chunk := pub.Size() - 11
	var out []byte
	for start := 0; start < len(plain); start += chunk {
		end := start + chunk
		if end > len(plain) {
			end = len(plain)
		}
		block, err := rsa.EncryptPKCS1v15(rand.Reader, pub, plain[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, block...)
	}
	return out, nil
}

// rsaPublicDecrypt 复刻 RSA_public_decrypt + PKCS#1 v1.5 去填充，
// 用于解开登录响应里的 encodeRes（Node 的 crypto.publicDecrypt）。
func rsaPublicDecrypt(encoded string, ciphertext string) ([]byte, error) {
	pub, err := parsePublicKey(encoded)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(ciphertext))
	if err != nil {
		return nil, err
	}
	if len(raw) != pub.Size() {
		return nil, errors.New("密文长度与公钥不匹配")
	}
	c := new(big.Int).SetBytes(raw)
	m := new(big.Int).Exp(c, big.NewInt(int64(pub.E)), pub.N)
	em := m.FillBytes(make([]byte, pub.Size()))
	if len(em) < 11 || em[0] != 0x00 || em[1] != 0x01 {
		return nil, errors.New("PKCS#1 填充不正确")
	}
	index := 2
	for index < len(em) && em[index] == 0xFF {
		index++
	}
	if index == 2 || index >= len(em) || em[index] != 0x00 {
		return nil, errors.New("PKCS#1 填充不正确")
	}
	return em[index+1:], nil
}
