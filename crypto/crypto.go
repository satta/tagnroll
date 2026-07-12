package crypto

import (
	"crypto/aes"
	"encoding/hex"
	"fmt"
	"strings"
)

// AES keys - set via SetKeys from config
var (
	aesKeyGen    []byte
	aesKeyCipher []byte
	keysSet      bool
)

// SetKeys sets the AES keys from printable strings
// Returns error if keys are invalid, but does not fail - keys will be validated on use
func SetKeys(keyGenStr, keyCipherStr string) error {
	keyGen := []byte(keyGenStr)
	if len(keyGen) != 16 {
		return fmt.Errorf("AES Key Gen must be 16 characters, got %d", len(keyGen))
	}

	keyCipher := []byte(keyCipherStr)
	if len(keyCipher) != 16 {
		return fmt.Errorf("AES Key Cipher must be 16 characters, got %d", len(keyCipher))
	}

	aesKeyGen = keyGen
	aesKeyCipher = keyCipher
	keysSet = true
	return nil
}

// GenerateKeyFromUID generates the authentication key from UID
// Matches JavaScript: uid.concat(uid).concat(uid).concat(uid) then AES encrypt
func GenerateKeyFromUID(uidHex string) (string, error) {
	if !keysSet {
		return "", fmt.Errorf("AES keys not set. Call SetKeys first")
	}

	// Clean up the input
	uidClean := strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(uidHex, " ", ""), ":", ""))

	// Validate hex characters
	_, err := hex.DecodeString(uidClean)
	if err != nil {
		return "", fmt.Errorf("invalid UID format: %w", err)
	}

	// Validate length (should be 4 or 7 bytes for MIFARE)
	if len(uidClean) != 8 && len(uidClean) != 14 {
		return "", fmt.Errorf("UID must be 8 or 14 hex characters, got %d", len(uidClean))
	}

	// Concatenate UID 4 times to make 16 bytes
	uidRepeated := uidClean + uidClean + uidClean + uidClean
	uidData, _ := hex.DecodeString(uidRepeated[:32]) // Take first 32 hex chars = 16 bytes

	// AES encrypt with AES_KEY_GEN using ECB mode
	cipher, err := aes.NewCipher(aesKeyGen)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	encrypted := make([]byte, 16)
	cipher.Encrypt(encrypted, uidData)

	// Return first 6 bytes as hex (first 12 hex characters)
	return strings.ToUpper(hex.EncodeToString(encrypted[:6])), nil
}

// StringToHex converts ASCII string to hex representation
func StringToHex(s string) string {
	return hex.EncodeToString([]byte(s))
}

// EncryptTagData encrypts tag data for writing to RFID
func EncryptTagData(asciiData string) (string, string, string, error) {
	if !keysSet {
		return "", "", "", fmt.Errorf("AES keys not set. Call SetKeys first")
	}

	// Convert ASCII string to hex representation
	hexData := StringToHex(asciiData)

	// Convert hex string to bytes
	data, err := hex.DecodeString(hexData)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to decode hex: %w", err)
	}

	// Should be exactly 48 bytes (3 blocks of 16)
	if len(data) != 48 {
		// Pad or truncate
		if len(data) < 48 {
			padding := make([]byte, 48-len(data))
			data = append(data, padding...)
		} else {
			data = data[:48]
		}
	}

	// AES encrypt with AES_KEY_CIPHER using ECB mode
	cipher, err := aes.NewCipher(aesKeyCipher)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to create cipher: %w", err)
	}

	encrypted := make([]byte, 48)
	for i := 0; i < 3; i++ {
		cipher.Encrypt(encrypted[i*16:(i+1)*16], data[i*16:(i+1)*16])
	}

	// Split into 3 blocks of 16 bytes
	block1 := strings.ToUpper(hex.EncodeToString(encrypted[0:16]))
	block2 := strings.ToUpper(hex.EncodeToString(encrypted[16:32]))
	block3 := strings.ToUpper(hex.EncodeToString(encrypted[32:48]))

	return block1, block2, block3, nil
}

// DecryptTagData decrypts tag data read from RFID
func DecryptTagData(block1Hex, block2Hex, block3Hex string) (string, string, error) {
	if !keysSet {
		return "", "", fmt.Errorf("AES keys not set. Call SetKeys first")
	}

	block1, _ := hex.DecodeString(strings.ReplaceAll(block1Hex, " ", ""))
	block2, _ := hex.DecodeString(strings.ReplaceAll(block2Hex, " ", ""))
	block3, _ := hex.DecodeString(strings.ReplaceAll(block3Hex, " ", ""))

	encryptedData := append(block1, block2...)
	encryptedData = append(encryptedData, block3...)

	cipher, err := aes.NewCipher(aesKeyCipher)
	if err != nil {
		return "", "", fmt.Errorf("failed to create cipher: %w", err)
	}

	decrypted := make([]byte, 48)
	for i := 0; i < 3; i++ {
		cipher.Decrypt(decrypted[i*16:(i+1)*16], encryptedData[i*16:(i+1)*16])
	}

	// Convert back from hex representation to ASCII
	hexStr := strings.ToUpper(hex.EncodeToString(decrypted))
	asciiStr := ""
	for i := 0; i < len(hexStr); i += 2 {
		b, _ := hex.DecodeString(hexStr[i : i+2])
		asciiStr += string(b)
	}

	return asciiStr, hexStr, nil
}

// TagData represents the structure of tag data
type TagData struct {
	Batch    string
	Date     string
	Supplier string
	Material string
	Color    string
	Length   string
	Serial   string
	Reserve  string
}

// BuildTagData builds the tag data string to match HTML format exactly
// Format: batch(3) + date(5) + supplier(4) + material(5) + color(7) + length(4) + serial(6) + reserve(14)
// Total: 48 characters
func BuildTagData(tag TagData) (string, error) {
	// Validate lengths
	if len(tag.Batch) != 3 {
		return "", fmt.Errorf("batch must be 3 characters, got %d", len(tag.Batch))
	}
	if len(tag.Date) != 5 {
		return "", fmt.Errorf("date must be 5 characters (YYMDD), got %d", len(tag.Date))
	}
	if len(tag.Supplier) != 4 {
		return "", fmt.Errorf("supplier must be 4 characters, got %d", len(tag.Supplier))
	}
	if len(tag.Material) != 5 {
		return "", fmt.Errorf("material must be 5 characters, got %d", len(tag.Material))
	}
	if len(tag.Color) != 7 {
		return "", fmt.Errorf("color must be 7 characters (0RRGGBB), got %d", len(tag.Color))
	}
	if len(tag.Length) != 4 {
		return "", fmt.Errorf("length must be 4 characters, got %d", len(tag.Length))
	}
	if len(tag.Serial) != 6 {
		return "", fmt.Errorf("serial must be 6 characters, got %d", len(tag.Serial))
	}
	if len(tag.Reserve) != 14 {
		return "", fmt.Errorf("reserve must be 14 characters, got %d", len(tag.Reserve))
	}

	tagData := tag.Batch + tag.Date + tag.Supplier + tag.Material + tag.Color + tag.Length + tag.Serial + tag.Reserve
	return tagData, nil
}

// ParseTagData parses decrypted tag data
func ParseTagData(asciiData string) (TagData, error) {
	if len(asciiData) < 48 {
		return TagData{}, fmt.Errorf("data too short: %d characters", len(asciiData))
	}

	return TagData{
		Batch:    asciiData[0:3],
		Date:     asciiData[3:8],
		Supplier: asciiData[8:12],
		Material: asciiData[12:17],
		Color:    asciiData[17:24],
		Length:   asciiData[24:28],
		Serial:   asciiData[28:34],
		Reserve:  asciiData[34:48],
	}, nil
}

// Material codes mapping
var MaterialCodes = map[string]string{
	"10001": "HP-TPU",
	"11001": "CR-Nylon",
	"13001": "CR-PLACarbon",
	"14001": "CR-PLAMatte",
	"15001": "CR-PLAFluo",
	"16001": "CR-TPU",
	"17001": "CR-Wood",
	"18001": "HPUltraPLA",
	"19001": "HP-ASA",
	"07001": "CR-ABS",
	"06001": "CR-PETG",
	"04001": "CR-PLA",
	"05001": "CR-Silk",
	"09001": "EN-PLA+",
	"09002": "ENDERFASTPLA",
	"08001": "Ender-PLA",
	"00004": "GenericABS",
	"00007": "GenericASA",
	"00010": "GenericBVOH",
	"00012": "GenericHIPS",
	"00008": "GenericPA",
	"00009": "GenericPA-CF",
	"00015": "GenericPA6-CF",
	"00016": "GenericPAHT-CF",
	"00021": "GenericPC",
	"00020": "GenericPET",
	"00013": "GenericPET-CF",
	"00003": "GenericPETG",
	"00014": "GenericPETG-CF",
	"00001": "GenericPLA",
	"00006": "GenericPLA-CF",
	"00002": "GenericPLA-Silk",
	"00019": "GenericPP",
	"00017": "GenericPPS",
	"00018": "GenericPPS-CF",
	"00011": "GenericPVA",
	"00005": "GenericTPU",
	"03001": "HyperABS",
	"06002": "HyperPETG",
	"01001": "HyperPLA",
	"02001": "HyperPLA-CF",
}

// GetMaterialName returns the material name for a given code
func GetMaterialName(code string) string {
	if name, ok := MaterialCodes[code]; ok {
		return name
	}
	return "Unknown"
}

// Length to weight mapping
var LengthCodes = map[string]string{
	"0330": "1.0 kg",
	"0165": "0.5 kg",
}

// GetWeight returns the weight for a given length code
func GetWeight(code string) string {
	if weight, ok := LengthCodes[code]; ok {
		return weight
	}
	return "Unknown"
}
