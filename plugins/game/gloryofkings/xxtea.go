package gloryofkings

// XXTEA（Corrected Block TEA）按公开规范实现，参数与上游 utils/xxtea.js 完全一致：
// 明文长度内嵌在最后一个 32 位字里，密钥补齐到 4 个字。

const (
	xxteaDelta     uint32 = 0x9E3779B9
	xxteaWordBytes        = 4
	xxteaKeyWords         = 4
)

func bytesToWords(buffer []byte, withLength bool) []uint32 {
	words := make([]uint32, (len(buffer)+xxteaWordBytes-1)/xxteaWordBytes)
	for i := range words {
		at := i * xxteaWordBytes
		var word uint32
		for j := 0; j < xxteaWordBytes; j++ {
			if at+j < len(buffer) {
				word |= uint32(buffer[at+j]) << (8 * j)
			}
		}
		words[i] = word
	}
	if withLength {
		words = append(words, uint32(len(buffer)))
	}
	return words
}

func wordsToBytes(words []uint32, withLength bool) []byte {
	size := len(words) * xxteaWordBytes
	if withLength {
		declared := int(int32(words[len(words)-1]))
		if declared < 0 || declared > size-xxteaWordBytes {
			return nil
		}
		size = declared
	}
	out := make([]byte, size)
	for i := 0; i < size; i++ {
		out[i] = byte(words[i/4] >> (8 * (i % 4)))
	}
	return out
}

func toKeyWords(key []byte) []uint32 {
	words := bytesToWords(key, false)
	for len(words) < xxteaKeyWords {
		words = append(words, 0)
	}
	return words[:xxteaKeyWords]
}

func xxteaMix(sum, y, z uint32, p int, e uint32, keyWords []uint32) uint32 {
	a := (z>>5 ^ y<<2) + (y>>3 ^ z<<4)
	b := (sum ^ y) + (keyWords[(p&3)^int(e)] ^ z)
	return a ^ b
}

func xxteaEncrypt(data, key []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	words := bytesToWords(data, true)
	keyWords := toKeyWords(key)
	rounds := 6 + 52/len(words)
	last := len(words) - 1
	var sum uint32
	z := words[last]
	for round := 0; round < rounds; round++ {
		sum += xxteaDelta
		e := (sum >> 2) & 3
		for p := 0; p < last; p++ {
			y := words[p+1]
			words[p] += xxteaMix(sum, y, z, p, e, keyWords)
			z = words[p]
		}
		y0 := words[0]
		words[last] += xxteaMix(sum, y0, z, last, e, keyWords)
		z = words[last]
	}
	return wordsToBytes(words, false)
}

func xxteaDecrypt(data, key []byte) []byte {
	if len(data) == 0 {
		return nil
	}
	words := bytesToWords(data, false)
	keyWords := toKeyWords(key)
	last := len(words) - 1
	rounds := 6 + 52/len(words)
	sum := uint32(rounds) * xxteaDelta
	y := words[0]
	for sum != 0 {
		e := (sum >> 2) & 3
		for p := last; p > 0; p-- {
			z := words[p-1]
			words[p] -= xxteaMix(sum, y, z, p, e, keyWords)
			y = words[p]
		}
		zLast := words[last]
		words[0] -= xxteaMix(sum, y, zLast, 0, e, keyWords)
		y = words[0]
		sum -= xxteaDelta
	}
	return wordsToBytes(words, true)
}
