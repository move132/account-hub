package security

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

const passwordAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func RandomToken(bytes int) (string, error) {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

// RandomPassword returns a password with the format hub_<length random characters>.
func RandomPassword(length int) (string, error) {
	if length <= 0 {
		return "", fmt.Errorf("password length must be positive")
	}
	value := make([]byte, length)
	limit := byte(256 - (256 % len(passwordAlphabet)))
	for index := 0; index < len(value); {
		var randomByte [1]byte
		if _, err := rand.Read(randomByte[:]); err != nil {
			return "", err
		}
		if randomByte[0] >= limit {
			continue
		}
		value[index] = passwordAlphabet[int(randomByte[0])%len(passwordAlphabet)]
		index++
	}
	return "hub_" + string(value), nil
}

func Redact(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "token") || strings.Contains(lower, "session") || strings.Contains(lower, "password") || strings.Contains(lower, "cookie") || strings.Contains(lower, "secret") {
				result[key] = "••••••••"
				continue
			}
			result[key] = Redact(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = Redact(item)
		}
		return result
	default:
		return value
	}
}

func RandomID(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + "_" + hex.EncodeToString(value), nil
}

func HashToken(value string) string {
	hash := sha256.Sum256([]byte(value))
	return hex.EncodeToString(hash[:])
}

func MaskSecret(value string) string {
	if value == "" {
		return ""
	}
	if len(value) <= 8 {
		return "••••••••"
	}
	return value[:4] + "••••••••" + value[len(value)-4:]
}
