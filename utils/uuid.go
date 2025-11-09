package utils

import (
	"crypto/rand"
	"fmt"
)

// GenerateUUID generates a simple UUID v4
func GenerateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40 // Version 4
	b[8] = (b[8] & 0x3f) | 0x80 // Variant bits
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// GenerateRandomHex generates a random hexadecimal string of specified byte length
func GenerateRandomHex(byteLength int) string {
	b := make([]byte, byteLength)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}
