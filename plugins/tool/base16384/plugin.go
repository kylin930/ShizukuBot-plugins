// Package base16384 移植 ZeroBot 的 base16384 编解码与 TEA 加解密。
//
// base16384 每 7 字节编码为 4 个码位，码位取 0x4E00 起的 CJK 字符；
// 不足 7 字节时按 14 位一组补齐，并追加一个 0x3D00|偏移 的标记码位。
// 上游把编码结果按 UTF-16BE 解释为字符串，这里用 unicode/utf16 复现。
//
// 带密钥模式与上游完全一致：密钥经 floatbox/crypto.GetTEA 截断或循环补齐到
// 16 字节，再交给 fumiama/gofastTEA 的自定义填充分组与双 IV 链式算法。
package base16384

import (
	"context"
	"encoding/binary"
	"math/rand/v2"
	"regexp"
	"unicode/utf16"
	"unicode/utf8"

	"shizukubot-plugins/internal/triggercmd"
	"shizukubot/sdk"
)

const (
	// chunkBase 是每个 14 位数据码位的起始值。
	chunkBase = 0x4E00
	// markerBase 是尾部偏移标记码位的起始值，低 8 位存放 1~6 的偏移。
	markerBase = 0x3D00
)

var (
	reKeyEncrypt = regexp.MustCompile(`^用(.+)加密\s*(.+)$`)
	reKeyDecrypt = regexp.MustCompile(`^用(.+)解密\s*([一-踀]+[㴁-㴆]?)$`)
	reEncrypt    = regexp.MustCompile(`^加密\s*(.+)$`)
	reDecrypt    = regexp.MustCompile(`^解密\s*([一-踀]+[㴁-㴆]?)$`)
)

// Definition 订阅「加密」「解密」「用密钥加密」「用密钥解密」四类指令。
func Definition() sdk.Definition {
	return triggercmd.EventPlugin(sdk.Meta{
		ID:          "base16384",
		Name:        "base16384 加解密",
		Version:     "1.0.0",
		Description: "base16384 编解码与 TEA 加密：加密xxx / 解密xxx / 用yyy加密xxx / 用yyy解密xxx",
	}, sdk.MessageEvents, Handle, []triggercmd.Command{
		{Name: "base16384", Trigger: "加密"},
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
	enc := encodeString(plain)
	if enc == "" {
		return c.Reply(ctx, "加密失败!")
	}
	return c.Reply(ctx, enc)
}

func handleDecrypt(ctx context.Context, c sdk.Call, cipher string) error {
	plain, ok := decodeString(cipher)
	if !ok {
		return c.Reply(ctx, "解密失败!")
	}
	return c.Reply(ctx, plain)
}

func handleKeyEncrypt(ctx context.Context, c sdk.Call, key, plain string) error {
	enc := utf16BEToString(encodeBase16384(getTEA(key).encrypt([]byte(plain))))
	if enc == "" {
		return c.Reply(ctx, "加密失败!")
	}
	return c.Reply(ctx, enc)
}

func handleKeyDecrypt(ctx context.Context, c sdk.Call, key, cipher string) error {
	raw, ok := decodeBytes(cipher)
	if !ok {
		return c.Reply(ctx, "解密失败!")
	}
	out := getTEA(key).decrypt(raw)
	if out == nil || !utf8.Valid(out) {
		return c.Reply(ctx, "解密失败!")
	}
	return c.Reply(ctx, string(out))
}

// encodeString 把文本的 UTF-8 字节编码后按 UTF-16BE 解释为字符串。
func encodeString(s string) string {
	return utf16BEToString(encodeBase16384([]byte(s)))
}

// decodeString 把字符串按 UTF-16BE 取字节后解码为 UTF-8 文本。
func decodeString(s string) (string, bool) {
	b, ok := decodeBytes(s)
	if !ok || !utf8.Valid(b) {
		return "", false
	}
	return string(b), true
}

// decodeBytes 只做 base16384 解码，不校验结果是否为合法 UTF-8；
// 带密钥模式下解码结果是任意密文字节。
func decodeBytes(s string) ([]byte, bool) {
	return decodeBase16384(utf16BEFromString(s))
}

// utf16BEToString 把 UTF-16BE 字节流转成 Go 字符串，与 x/text 的
// unicode.UTF16(BigEndian, IgnoreBOM) 解码结果一致。
func utf16BEToString(b []byte) string {
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = binary.BigEndian.Uint16(b[i*2:])
	}
	return string(utf16.Decode(units))
}

// utf16BEFromString 把字符串转成 UTF-16BE 字节流（非 BMP 字符会拆成代理对）。
func utf16BEFromString(s string) []byte {
	units := utf16.Encode([]rune(s))
	out := make([]byte, len(units)*2)
	for i, u := range units {
		binary.BigEndian.PutUint16(out[i*2:], u)
	}
	return out
}

// encodeBase16384 按 7 字节 → 4 码位编码，尾部补偏移标记。
func encodeBase16384(b []byte) []byte {
	blocks := len(b) / 7
	offset := len(b) % 7
	units := make([]uint16, 0, blocks*4+5)
	for i := 0; i < blocks; i++ {
		var v uint64
		for j := 0; j < 7; j++ {
			v = v<<8 | uint64(b[i*7+j])
		}
		units = append(units,
			uint16(chunkBase+v>>42),
			uint16(chunkBase+(v>>28)&0x3fff),
			uint16(chunkBase+(v>>14)&0x3fff),
			uint16(chunkBase+v&0x3fff),
		)
	}
	if offset > 0 {
		var v uint64
		for j := 0; j < offset; j++ {
			v = v<<8 | uint64(b[blocks*7+j])
		}
		bits := offset * 8
		chunks := (bits + 13) / 14
		for i := 0; i < chunks; i++ {
			shift := bits - 14*(i+1)
			var chunk uint64
			if shift >= 0 {
				chunk = (v >> uint(shift)) & 0x3fff
			} else {
				chunk = (v << uint(-shift)) & 0x3fff
			}
			units = append(units, uint16(chunkBase+chunk))
		}
		units = append(units, uint16(markerBase+offset))
	}
	out := make([]byte, len(units)*2)
	for i, u := range units {
		binary.BigEndian.PutUint16(out[i*2:], u)
	}
	return out
}

// decodeBase16384 是 encodeBase16384 的逆过程，非法结构返回 false。
func decodeBase16384(b []byte) ([]byte, bool) {
	if len(b) == 0 || len(b)%2 != 0 {
		return nil, false
	}
	units := make([]uint16, len(b)/2)
	for i := range units {
		units[i] = binary.BigEndian.Uint16(b[i*2:])
	}
	offset := 0
	if last := units[len(units)-1]; last&0xff00 == markerBase {
		if o := int(last & 0xff); o >= 1 && o <= 6 {
			offset = o
			units = units[:len(units)-1]
		}
	}
	blocks := len(units) / 4
	rest := len(units) % 4
	if offset == 0 && rest != 0 {
		return nil, false
	}
	if offset > 0 && rest != (offset*8+13)/14 {
		return nil, false
	}
	out := make([]byte, 0, blocks*7+offset)
	for i := 0; i < blocks; i++ {
		c0, ok0 := chunkValue(units[i*4])
		c1, ok1 := chunkValue(units[i*4+1])
		c2, ok2 := chunkValue(units[i*4+2])
		c3, ok3 := chunkValue(units[i*4+3])
		if !ok0 || !ok1 || !ok2 || !ok3 {
			return nil, false
		}
		v := c0<<42 | c1<<28 | c2<<14 | c3
		for j := 0; j < 7; j++ {
			out = append(out, byte(v>>uint(48-8*j)))
		}
	}
	if offset > 0 {
		var v uint64
		for i := 0; i < rest; i++ {
			chunk, ok := chunkValue(units[blocks*4+i])
			if !ok {
				return nil, false
			}
			v = v<<14 | chunk
		}
		v >>= uint(rest*14 - offset*8)
		for j := offset - 1; j >= 0; j-- {
			out = append(out, byte(v>>uint(8*j)))
		}
	}
	return out, true
}

func chunkValue(u uint16) (uint64, bool) {
	v := uint64(u)
	if v < chunkBase || v > chunkBase+0x3fff {
		return 0, false
	}
	return v - chunkBase, true
}

const teaDelta = 0x9E3779B9

// randUint32 为 TEA 填充分组提供随机字节，测试中可替换以复现固定密文。
// 上游 gofastTEA 在 Go 1.22+ 使用 math/rand/v2。
var randUint32 = rand.Uint32

// teaCipher 是 gofastTEA 的 4 个 32 位子密钥。
type teaCipher [4]uint32

// getTEA 复现 floatbox/crypto.GetTEA：原始 key 字节超过 16 字节时截断，
// 不足时循环取当前 key 的前缀补齐到 16 字节，再按大端拆成 4 个子密钥。
func getTEA(key string) teaCipher {
	if len(key) == 0 {
		return teaCipher{}
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
	var t teaCipher
	for i := 0; i < 4; i++ {
		t[i] = binary.BigEndian.Uint32([]byte(key[i*4:]))
	}
	return t
}

// encrypt 复现 gofastTEA 的加密：头部存放随机字节与填充长度，
// 每个 8 字节分组先与上一分组异或，再用双 IV 链式加密。
func (t teaCipher) encrypt(src []byte) []byte {
	lens := len(src)
	fill := 10 - (lens+1)%8
	dst := make([]byte, fill+lens+7)
	binary.LittleEndian.PutUint32(dst, randUint32())
	binary.LittleEndian.PutUint32(dst[4:], randUint32())
	binary.LittleEndian.PutUint32(dst[8:], randUint32())
	dst[0] = byte(fill-3) | 0xF8 // 低 3 位即填充长度减 3
	copy(dst[fill:], src)

	var iv1, iv2, holder uint64
	for i := 0; i < len(dst); i += 8 {
		block := binary.BigEndian.Uint64(dst[i:])
		holder = block ^ iv1
		iv1 = t.encode(holder) ^ iv2
		iv2 = holder
		binary.BigEndian.PutUint64(dst[i:], iv1)
	}
	return dst
}

// decrypt 还原 encrypt 的链式分组并去掉头部填充；结构非法时返回 nil。
func (t teaCipher) decrypt(data []byte) []byte {
	if len(data) < 16 || len(data)%8 != 0 {
		return nil
	}
	dst := make([]byte, len(data))
	var iv1, iv2, holder uint64
	for i := 0; i < len(dst); i += 8 {
		iv1 = binary.BigEndian.Uint64(data[i:])
		iv2 ^= iv1
		iv2 = t.decode(iv2)
		binary.BigEndian.PutUint64(dst[i:], iv2^holder)
		holder = iv1
	}
	// 头部首字节固定带 0xF8 标记，可用来快速剔除错误密钥。
	if dst[0]&0xF8 != 0xF8 {
		return nil
	}
	a, b := int(dst[0]&7+3), len(data)-7
	if a >= b {
		return nil
	}
	return dst[a:b]
}

// encode 是标准 TEA 的一个 8 字节分组加密（16 轮）。
func (t teaCipher) encode(n uint64) uint64 {
	v0, v1 := uint32(n>>32), uint32(n)
	var sum uint32
	for i := 0; i < 16; i++ {
		sum += teaDelta
		v0 += ((v1 << 4) + t[0]) ^ (v1 + sum) ^ ((v1 >> 5) + t[1])
		v1 += ((v0 << 4) + t[2]) ^ (v0 + sum) ^ ((v0 >> 5) + t[3])
	}
	return uint64(v0)<<32 | uint64(v1)
}

// decode 是 encode 的逆运算。
func (t teaCipher) decode(n uint64) uint64 {
	v0, v1 := uint32(n>>32), uint32(n)
	sum := uint32(teaDelta)
	sum *= 16
	for i := 0; i < 16; i++ {
		v1 -= ((v0 << 4) + t[2]) ^ (v0 + sum) ^ ((v0 >> 5) + t[3])
		v0 -= ((v1 << 4) + t[0]) ^ (v1 + sum) ^ ((v1 >> 5) + t[1])
		sum -= teaDelta
	}
	return uint64(v0)<<32 | uint64(v1)
}
