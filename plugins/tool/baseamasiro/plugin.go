// Package baseamasiro 移植 ZeroBot 的「阿玛希罗 Base」编解码与 TEA 加解密。
//
// 字符表为 U+0900..U+097F 的天城文 128 个码位（即 unibase2n.BaseDevanagari），
// 每个字符承载 7 bit。编码把输入当作连续的比特流，按 7 bit 一组从高位向低位切分；
// 末尾不足 7 bit 的部分补零凑满一个字符，再追加一个偏移标记 U+09E5+余字节数
// （落点正好是孟加拉数字 ০..৫，与上游一致），解码时据此还原原文长度。
// 带密钥模式沿用上游 floatbox crypto.GetTEA + gofastTEA 的流程：
// 12 字节随机头（首字节记录填充长度）、填充到 8 字节对齐、CBC 式链式加密。
package baseamasiro

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
	// devBase 是天城文字符表的起始码位（ऀ）。
	devBase = 0x0900
	// devTailBase 是偏移标记的起始码位，实际写入 devTailBase+余字节数。
	devTailBase = 0x09E5
)

var (
	reKeyEncrypt = regexp.MustCompile(`^天城文用(.+)加密\s*(.+)$`)
	reKeyDecrypt = regexp.MustCompile(`^天城文用(.+)解密\s*([ऀ-ॿ]+[০-৫]?)$`)
	reEncrypt    = regexp.MustCompile(`^天城文加密\s*(.+)$`)
	reDecrypt    = regexp.MustCompile(`^天城文解密\s*([ऀ-ॿ]+[০-৫]?)$`)
)

// Definition 订阅「加密」「解密」「用密钥加密」「用密钥解密」四类指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "baseamasiro",
		Name:        "天城文加解密",
		Version:     "1.0.0",
		Description: "天城文编码与 TEA 加密：天城文加密xxx / 天城文解密xxx / 天城文用yyy加密xxx / 天城文用yyy解密xxx",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "baseamasiro", Trigger: "天城文加密"},
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
	enc := encodeDevanagari([]byte(plain))
	if enc == "" {
		return c.Reply(ctx, "加密失败!")
	}
	return c.Reply(ctx, enc)
}

func handleDecrypt(ctx context.Context, c sdk.Call, cipher string) error {
	plain, ok := decodeDevanagari(cipher)
	if !ok || len(plain) == 0 || !utf8.Valid(plain) {
		return c.Reply(ctx, "解密失败!")
	}
	return c.Reply(ctx, string(plain))
}

func handleKeyEncrypt(ctx context.Context, c sdk.Call, key, plain string) error {
	enc := encodeDevanagari(teaEncrypt(key, []byte(plain)))
	if enc == "" {
		return c.Reply(ctx, "加密失败!")
	}
	return c.Reply(ctx, enc)
}

func handleKeyDecrypt(ctx context.Context, c sdk.Call, key, cipher string) error {
	raw, ok := decodeDevanagari(cipher)
	if !ok {
		return c.Reply(ctx, "解密失败!")
	}
	out, ok := teaDecrypt(key, raw)
	if !ok || len(out) == 0 || !utf8.Valid(out) {
		return c.Reply(ctx, "解密失败!")
	}
	return c.Reply(ctx, string(out))
}

// encodeDevanagari 把任意字节编码为天城文字符串。
// 每 7 bit 一个字符，末尾不足 7 bit 补零，非整 7 字节时追加偏移标记。
func encodeDevanagari(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	out := make([]rune, 0, len(data)/7*8+8)
	var acc uint32
	var nbits uint
	for _, b := range data {
		acc = acc<<8 | uint32(b)
		nbits += 8
		for nbits >= 7 {
			nbits -= 7
			out = append(out, rune(devBase+int(acc>>nbits&0x7f)))
			if nbits == 0 {
				acc = 0
			} else {
				acc &= 1<<nbits - 1
			}
		}
	}
	if nbits > 0 {
		out = append(out, rune(devBase+int(acc<<(7-nbits)&0x7f)))
	}
	if rest := len(data) % 7; rest != 0 {
		out = append(out, rune(devTailBase+rest))
	}
	return string(out)
}

// decodeDevanagari 是 encodeDevanagari 的逆过程，非法结构返回 false。
func decodeDevanagari(s string) ([]byte, bool) {
	runes := []rune(s)
	if len(runes) == 0 {
		return nil, false
	}
	offset := 0
	if last := runes[len(runes)-1]; last > devTailBase && last <= devTailBase+6 {
		offset = int(last - devTailBase)
		runes = runes[:len(runes)-1]
	}
	if len(runes) == 0 {
		return nil, false
	}
	// dataChars 是承载比特的字符数，其余必须是完整的 8 字符组。
	dataChars := 0
	if offset != 0 {
		dataChars = offset*8/7 + 1
		if len(runes) < dataChars || (len(runes)-dataChars)%8 != 0 {
			return nil, false
		}
	} else if len(runes)%8 != 0 {
		return nil, false
	}
	outLen := (len(runes)-dataChars)/8*7 + offset
	if outLen <= 0 {
		return nil, false
	}
	out := make([]byte, 0, outLen)
	var acc uint32
	var nbits uint
	for _, r := range runes {
		if r < devBase || r >= devBase+128 {
			return nil, false
		}
		acc = acc<<7 | uint32(r-devBase)
		nbits += 7
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
