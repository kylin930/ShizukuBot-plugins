// Package base64gua 移植 ZeroBot 的六十四卦（base64gua）编解码与 TEA 加解密。
//
// 字符表为 U+4DC0..U+4DFF 的六十四卦（䷀..䷿），每个字符承载 6 bit。
// 编码按标准 base64 的位打包方式处理：3 字节 → 4 个卦符；不足 3 字节时补零，
// 并与上游 unibase2n 一致，把该组最后一个卦符替换为偏移标记：
// 余 1 字节 → 2 个卦符 + ☰（U+2630），余 2 字节 → 3 个卦符 + ☱（U+2631）。
// 解码时标记按 6 个零 bit 参与位流，因此可与上游结果严格互通。
package base64gua

import (
	"context"
	"encoding/binary"
	"math/rand/v2"
	"regexp"
	"unicode/utf8"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// guaBase 是六十四卦字符表的起始码位（䷀）。
	guaBase = 0x4DC0
	// guaTail1 / guaTail2 是上游 unibase2n 用于标记「余 1 / 余 2 字节」的码位。
	guaTail1 = 0x2630 // ☰
	guaTail2 = 0x2631 // ☱
)

var (
	reKeyEncrypt = regexp.MustCompile(`^六十四卦用(.+)加密\s*(.+)$`)
	reKeyDecrypt = regexp.MustCompile(`^六十四卦用(.+)解密\s*([䷀-䷿]+[☰☱]?)$`)
	reEncrypt    = regexp.MustCompile(`^六十四卦加密\s*(.+)$`)
	reDecrypt    = regexp.MustCompile(`^六十四卦解密\s*([䷀-䷿]+[☰☱]?)$`)
)

// Definition 订阅「加密」「解密」「用密钥加密」「用密钥解密」四类指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "base64gua",
		Name:        "六十四卦加解密",
		Version:     "1.0.0",
		Description: "六十四卦编解码与 TEA 加密：六十四卦加密xxx / 六十四卦解密xxx / 六十四卦用yyy加密xxx / 六十四卦用yyy解密xxx",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "base64gua", Trigger: "六十四卦加密"},
	})
}

// Handle 按指令前缀分派到对应的编解码逻辑。
func Handle(ctx context.Context, c sdk.Call) error {
	text := sdk.Text(c.Event)
	if m := sdk.MatchRegexp(text, reKeyEncrypt); len(m) == 3 {
		return handleKeyEncrypt(ctx, c, m[1], m[2])
	}
	if m := sdk.MatchRegexp(text, reKeyDecrypt); len(m) == 3 {
		return handleKeyDecrypt(ctx, c, m[1], m[2])
	}
	if m := sdk.MatchRegexp(text, reEncrypt); len(m) == 2 {
		return handleEncrypt(ctx, c, m[1])
	}
	if m := sdk.MatchRegexp(text, reDecrypt); len(m) == 2 {
		return handleDecrypt(ctx, c, m[1])
	}
	return nil
}

func handleEncrypt(ctx context.Context, c sdk.Call, plain string) error {
	enc := encodeGuaString([]byte(plain))
	if enc == "" {
		return c.Reply(ctx, "加密失败!")
	}
	return c.Reply(ctx, enc)
}

func handleDecrypt(ctx context.Context, c sdk.Call, cipher string) error {
	plain, ok := decodeGua(cipher)
	if !ok || !utf8.Valid(plain) {
		return c.Reply(ctx, "解密失败!")
	}
	return c.Reply(ctx, string(plain))
}

func handleKeyEncrypt(ctx context.Context, c sdk.Call, key, plain string) error {
	enc := encodeGuaString(teaEncrypt(key, []byte(plain)))
	if enc == "" {
		return c.Reply(ctx, "加密失败!")
	}
	return c.Reply(ctx, enc)
}

func handleKeyDecrypt(ctx context.Context, c sdk.Call, key, cipher string) error {
	raw, ok := decodeGua(cipher)
	if !ok {
		return c.Reply(ctx, "解密失败!")
	}
	out, ok := teaDecrypt(key, raw)
	if !ok || !utf8.Valid(out) {
		return c.Reply(ctx, "解密失败!")
	}
	return c.Reply(ctx, string(out))
}

// encodeGuaString 把任意字节编码为六十四卦字符串。
func encodeGuaString(data []byte) string {
	return string(encodeGua(data))
}

// encodeGua 按 3 字节 → 4 卦符打包。余 1 字节时输出 2 个卦符 + ☰，
// 余 2 字节时输出 3 个卦符 + ☱，与上游 unibase2n 的截断方式一致。
func encodeGua(data []byte) []rune {
	if len(data) == 0 {
		return nil
	}
	groups := len(data) / 3
	rest := len(data) % 3
	capHint := groups * 4
	if rest != 0 {
		capHint += 4
	}
	out := make([]rune, 0, capHint)
	for i := 0; i < groups; i++ {
		b := data[i*3 : i*3+3]
		out = appendGroup(out, uint32(b[0])<<16|uint32(b[1])<<8|uint32(b[2]))
	}
	switch rest {
	case 1:
		b := data[groups*3]
		v := uint32(b) << 16
		out = append(out,
			rune(guaBase+int(v>>18&0x3f)),
			rune(guaBase+int(v>>12&0x3f)),
			rune(guaTail1),
		)
	case 2:
		b0, b1 := data[groups*3], data[groups*3+1]
		out = appendGroup(out, uint32(b0)<<16|uint32(b1)<<8)
		out[len(out)-1] = guaTail2
	}
	return out
}

// appendGroup 把 24 bit 拆成 4 个 6 bit 卦符。
func appendGroup(out []rune, v uint32) []rune {
	return append(out,
		rune(guaBase+int(v>>18&0x3f)),
		rune(guaBase+int(v>>12&0x3f)),
		rune(guaBase+int(v>>6&0x3f)),
		rune(guaBase+int(v&0x3f)),
	)
}

// decodeGua 是 encodeGua 的逆过程，非法结构返回 false。
func decodeGua(s string) ([]byte, bool) {
	runes := []rune(s)
	if len(runes) == 0 {
		return nil, false
	}
	offset := 0
	switch runes[len(runes)-1] {
	case guaTail1:
		offset = 1
	case guaTail2:
		offset = 2
	}
	n := len(runes)
	var outLen int
	switch offset {
	case 0:
		if n%4 != 0 {
			return nil, false
		}
		outLen = n / 4 * 3
	case 1:
		if n%4 != 3 {
			return nil, false
		}
		outLen = n/4*3 + 1
	case 2:
		if n%4 != 0 {
			return nil, false
		}
		outLen = n/4*3 - 1
	}
	out := make([]byte, 0, outLen)
	var acc uint64
	var nbits uint
	for i, r := range runes {
		var v uint64
		if offset != 0 && i == n-1 {
			// 尾部标记与上游一致，按 0 参与位流。
			v = 0
		} else if r >= guaBase && r <= guaBase+63 {
			v = uint64(r - guaBase)
		} else {
			return nil, false
		}
		acc = acc<<6 | v
		nbits += 6
		if nbits >= 8 {
			nbits -= 8
			out = append(out, byte(acc>>nbits))
			if nbits == 0 {
				acc = 0
			} else {
				acc &= 1<<nbits - 1
			}
		}
	}
	if len(out) < outLen {
		return nil, false
	}
	return out[:outLen], true
}

const teaDelta = 0x9E3779B9

// teaKey 复刻 floatbox 的 crypto.GetTEA：超过 16 字节截断，不足则循环补足，
// 再按大端拆成 4 个 uint32。
func teaKey(key string) (t [4]uint32) {
	if len(key) == 0 {
		return t
	}
	if len(key) > 16 {
		key = key[:16]
	} else {
		for len(key) < 16 {
			n := 16 - len(key)
			if n > len(key) {
				n = len(key)
			}
			key += key[:n]
		}
	}
	for i := 0; i < 4; i++ {
		t[i] = binary.BigEndian.Uint32([]byte(key[i*4 : i*4+4]))
	}
	return t
}

// teaEncrypt 复刻 floatbox 所依赖的 gofastTEA.Encrypt：
// 12 字节随机头（首字节写入填充长度）、填充到 8 字节对齐、CBC 式链式加密。
// 这里没有采用「md5(key) + PKCS#7」的替代方案，以便与上游密文互通。
func teaEncrypt(key string, src []byte) []byte {
	lens := len(src)
	fill := 10 - (lens+1)%8
	dst := make([]byte, fill+lens+7)
	binary.LittleEndian.PutUint32(dst[0:], rand.Uint32())
	binary.LittleEndian.PutUint32(dst[4:], rand.Uint32())
	binary.LittleEndian.PutUint32(dst[8:], rand.Uint32())
	dst[0] = byte(fill-3) | 0xF8 // 低 3 位存储填充长度
	copy(dst[fill:], src)

	t := teaKey(key)
	var iv1, iv2, holder uint64
	for i := 0; i < len(dst); i += 8 {
		block := binary.BigEndian.Uint64(dst[i:])
		holder = block ^ iv1
		iv1 = teaEncodeBlock(t, holder)
		iv1 ^= iv2
		iv2 = holder
		binary.BigEndian.PutUint64(dst[i:], iv1)
	}
	return dst
}

// teaDecrypt 复刻 gofastTEA.Decrypt，并按首字节还原真实明文。
func teaDecrypt(key string, data []byte) ([]byte, bool) {
	if len(data) < 16 || len(data)%8 != 0 {
		return nil, false
	}
	dst := make([]byte, len(data))
	t := teaKey(key)
	var iv1, iv2, holder uint64
	for i := 0; i < len(dst); i += 8 {
		iv1 = binary.BigEndian.Uint64(data[i:])
		iv2 ^= iv1
		iv2 = teaDecodeBlock(t, iv2)
		binary.BigEndian.PutUint64(dst[i:], iv2^holder)
		holder = iv1
	}
	from, to := int(dst[0]&7+3), len(data)-7
	if from >= to {
		return nil, false
	}
	return dst[from:to], true
}

// teaEncodeBlock 对 8 字节分组做标准 TEA 16 轮加密。
func teaEncodeBlock(t [4]uint32, n uint64) uint64 {
	v0, v1 := uint32(n>>32), uint32(n)
	var sum uint32
	for i := 0; i < 16; i++ {
		sum += teaDelta
		v0 += (v1 + sum) ^ ((v1 << 4) + t[0]) ^ ((v1 >> 5) + t[1])
		v1 += (v0 + sum) ^ ((v0 << 4) + t[2]) ^ ((v0 >> 5) + t[3])
	}
	return uint64(v0)<<32 | uint64(v1)
}

// teaDecodeBlock 是 teaEncodeBlock 的逆过程。
func teaDecodeBlock(t [4]uint32, n uint64) uint64 {
	v0, v1 := uint32(n>>32), uint32(n)
	sum := uint32(0xE3779B90) // 16 * teaDelta（按 32 位回绕）
	for i := 0; i < 16; i++ {
		v1 -= (v0 + sum) ^ ((v0 << 4) + t[2]) ^ ((v0 >> 5) + t[3])
		v0 -= (v1 + sum) ^ ((v1 << 4) + t[0]) ^ ((v1 >> 5) + t[1])
		sum -= teaDelta
	}
	return uint64(v0)<<32 | uint64(v1)
}
