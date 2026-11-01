package fleet

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
)

const protocolGCM = "sms-air780-v1"
const protocolCBC = "sms-air724-v2"

func normalizeProtocol(p string) string {
	if p == "" {
		return protocolGCM
	}
	return p
}
func supportedProtocol(p string) bool { return p == protocolGCM || p == protocolCBC }
func protocolForVersion(v int) string {
	if v == 1 {
		return protocolGCM
	}
	if v == 2 {
		return protocolCBC
	}
	return "unsupported"
}
func deriveCBC(root []byte, sid, direction, purpose string) []byte {
	mac := hmac.New(sha256.New, root)
	mac.Write([]byte(protocolCBC + "|" + sid + "|" + direction + "|" + purpose))
	return mac.Sum(nil)
}

// MAC covers every outer field except tag. Each UTF-8 string is length-prefixed
// in bytes, with no separators outside the prefixes (e.g. 1:2...).
func cbcMAC(root []byte, p Envelope) []byte {
	mac := hmac.New(sha256.New, deriveCBC(root, p.SID, p.Direction, "mac"))
	for _, value := range []string{strconv.Itoa(p.V), p.DeviceID, p.SID, p.Direction, strconv.FormatUint(p.Seq, 10), p.Kind, p.KeyID, p.WrappedKey, p.IV, p.Ciphertext} {
		mac.Write([]byte(strconv.Itoa(len(value)) + ":" + value))
	}
	return mac.Sum(nil)
}
func decodeCanonicalHex(value string, size int) ([]byte, error) {
	b, err := hex.DecodeString(value)
	if err != nil || value != hex.EncodeToString(b) || (size >= 0 && len(b) != size) {
		return nil, errPacket
	}
	return b, nil
}
func decryptCBC(root []byte, p Envelope, direction string) (json.RawMessage, error) {
	if len(root) != 32 || p.V != 2 || !validID.MatchString(p.DeviceID) || p.Direction != direction || (direction != "up" && direction != "down") || p.Seq < 1 || p.Seq > 2147483647 || len(p.Ciphertext) > 65536 {
		return nil, errPacket
	}
	if _, err := decodeCanonicalHex(p.SID, 16); err != nil {
		return nil, errPacket
	}
	if p.Kind != "register" && p.Kind != "event" && p.Kind != "command" && p.Kind != "ack" && p.Kind != "register_result" {
		return nil, errPacket
	}
	if p.Kind != "register" && (p.KeyID != "" || p.WrappedKey != "") {
		return nil, errPacket
	}
	iv, err := decodeCanonicalHex(p.IV, 16)
	if err != nil {
		return nil, errPacket
	}
	ct, err := decodeCanonicalHex(p.Ciphertext, -1)
	if err != nil || len(ct) == 0 || len(ct)%16 != 0 {
		return nil, errPacket
	}
	tag, err := decodeCanonicalHex(p.Tag, 32)
	if err != nil || !hmac.Equal(tag, cbcMAC(root, p)) {
		return nil, errPacket
	}
	// Encrypt-then-MAC: no decryption or padding inspection before authentication.
	block, err := aes.NewCipher(deriveCBC(root, p.SID, p.Direction, "enc"))
	if err != nil {
		return nil, errPacket
	}
	clear := make([]byte, len(ct))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(clear, ct)
	pad := int(clear[len(clear)-1])
	if pad < 1 || pad > 16 || pad > len(clear) {
		return nil, errPacket
	}
	if !bytes.Equal(clear[len(clear)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return nil, errPacket
	}
	clear = clear[:len(clear)-pad]
	var inner innerPacket
	if json.Unmarshal(clear, &inner) != nil || inner.V != p.V || inner.DeviceID != p.DeviceID || inner.SID != p.SID || inner.Direction != p.Direction || inner.Seq != p.Seq || inner.Kind != p.Kind || len(inner.Body) == 0 || inner.Body[0] != '{' {
		return nil, errPacket
	}
	return inner.Body, nil
}
func encryptCBC(root []byte, session Session, kind string, body any) ([]byte, error) {
	if session.DownSeq < 1 || session.DownSeq > 2147483647 {
		return nil, errPacket
	}
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	clear, err := json.Marshal(innerPacket{V: 2, DeviceID: session.DeviceID, SID: session.ID, Direction: "down", Seq: session.DownSeq, Kind: kind, Body: b})
	if err != nil {
		return nil, err
	}
	iv := make([]byte, 16)
	if _, err = rand.Read(iv); err != nil {
		return nil, err
	}
	pad := 16 - len(clear)%16
	clear = append(clear, bytes.Repeat([]byte{byte(pad)}, pad)...)
	block, err := aes.NewCipher(deriveCBC(root, session.ID, "down", "enc"))
	if err != nil {
		return nil, err
	}
	ct := make([]byte, len(clear))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, clear)
	p := Envelope{V: 2, DeviceID: session.DeviceID, SID: session.ID, Direction: "down", Seq: session.DownSeq, Kind: kind, IV: hex.EncodeToString(iv), Ciphertext: hex.EncodeToString(ct)}
	p.Tag = hex.EncodeToString(cbcMAC(root, p))
	return json.Marshal(p)
}
func decryptSessionPacket(root []byte, p Envelope, session Session) (json.RawMessage, error) {
	protocol := normalizeProtocol(session.Protocol)
	if protocolForVersion(p.V) != protocol {
		return nil, errPacket
	}
	if protocol == protocolCBC {
		if strings.TrimSpace(session.AADMode) != "" {
			return nil, errPacket
		}
		return decryptCBC(root, p, "up")
	}
	return decryptPacketWithAAD(root, p, session.AADMode)
}
