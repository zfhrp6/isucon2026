package isureal

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

const imageURLLifetime = 24 * time.Hour
const imageSignatureIterations = 1000

func imageSigningKey(secretKeyHash string) ([]byte, error) {
	key, err := hex.DecodeString(secretKeyHash)
	if err != nil || len(key) != sha256.Size {
		return nil, fmt.Errorf("invalid image signing key")
	}
	return key, nil
}

func imageSignature(key []byte, imageID string, exp int64) []byte {
	signature := computeImageSignature(key, imageID, exp)
	return signature[:]
}

func computeImageSignature(key []byte, imageID string, exp int64) (signature [sha256.Size]byte) {
	value := []byte(strconv.FormatInt(exp, 10) + ":" + imageID)
	mac := hmac.New(sha256.New, key)
	sum := make([]byte, 0, sha256.Size)
	for range imageSignatureIterations {
		mac.Reset()
		mac.Write(value)
		sum = mac.Sum(sum[:0])
		value = sum
	}
	copy(signature[:], value)
	return signature
}

func signedImageURL(user User, imageID string) string {
	exp := imageURLExpiration()
	return signedImageURLWithExpiration(user, imageID, exp)
}

func imageURLExpiration() int64 {
	return time.Now().Add(imageURLLifetime).Unix()
}

func signedImageURLWithExpiration(user User, imageID string, exp int64) string {
	signature := processImageSignatures.signature(user.ImageKey, imageID, exp)
	query := url.Values{
		"exp": {strconv.FormatInt(exp, 10)},
		"sig": {base64.RawURLEncoding.EncodeToString(signature[:])},
	}
	return "/api/images/" + url.PathEscape(imageID) + "?" + query.Encode()
}

type profileImageURLKey struct {
	signingKey string
	imageID    string
	exp        int64
}

// profileImageURLSigner reuses profile URLs only within one response build.
type profileImageURLSigner struct {
	urls map[profileImageURLKey]string
}

func newProfileImageURLSigner() *profileImageURLSigner {
	return &profileImageURLSigner{urls: make(map[profileImageURLKey]string)}
}

func (signer *profileImageURLSigner) profileImageURL(user User) *string {
	if user.ProfileImageID == nil {
		return nil
	}
	return signer.profileImageURLAt(user, imageURLExpiration())
}

func (signer *profileImageURLSigner) profileImageURLAt(user User, exp int64) *string {
	if user.ProfileImageID == nil {
		return nil
	}
	// Expiry stays in the key because each call computes its own Unix-second
	// boundary; storing the completed URL string also avoids sharing signature buffers.
	key := profileImageURLKey{signingKey: string(user.ImageKey), imageID: *user.ProfileImageID, exp: exp}
	if cached, ok := signer.urls[key]; ok {
		return &cached
	}

	imageURL := signedImageURLWithExpiration(user, *user.ProfileImageID, exp)
	signer.urls[key] = imageURL
	return &imageURL
}
