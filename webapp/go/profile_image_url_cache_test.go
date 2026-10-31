package isureal

import (
	"strings"
	"testing"
)

func TestProfileImageURLSignerReusesOnlyIdenticalSigningInputs(t *testing.T) {
	signer := newProfileImageURLSigner()
	user := User{ImageKey: []byte("key-a"), ProfileImageID: stringPointer("profile-a")}

	first := signer.profileImageURLAt(user, 1760000000)
	second := signer.profileImageURLAt(user, 1760000000)
	if first == nil || second == nil || *first != *second {
		t.Fatalf("matching signing inputs returned different profile URLs: %v, %v", first, second)
	}

	for _, test := range []struct {
		name string
		user User
		exp  int64
	}{
		{name: "different key", user: User{ImageKey: []byte("key-b"), ProfileImageID: stringPointer("profile-a")}, exp: 1760000000},
		{name: "different image", user: User{ImageKey: []byte("key-a"), ProfileImageID: stringPointer("profile-b")}, exp: 1760000000},
		{name: "different expiry", user: user, exp: 1760000001},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := signer.profileImageURLAt(test.user, test.exp)
			if got == nil || *got == *first {
				t.Fatalf("different signing inputs reused the first URL: %v", got)
			}
		})
	}
}

func TestProfileImageURLSignerOmitsMissingProfileAndDoesNotReuseReactionImage(t *testing.T) {
	user := User{ImageKey: []byte("key-a")}
	if got := newProfileImageURLSigner().profileImageURL(user); got != nil {
		t.Fatalf("profileImageURL() = %q, want nil without a profile image", *got)
	}

	user.ProfileImageID = stringPointer("profile-a")
	signer := newProfileImageURLSigner()
	profile := signer.profileImageURLAt(user, 1760000000)
	reaction := signedImageURLWithExpiration(user, "reaction-a", 1760000000)
	if profile == nil || strings.Contains(*profile, "reaction-a") || strings.Contains(reaction, "profile-a") {
		t.Fatalf("profile URL and reaction image URL were mixed: profile=%v reaction=%q", profile, reaction)
	}
}

func TestProfileImageURLSignerCacheIsRequestLocalAndReturnedURLIsIndependent(t *testing.T) {
	user := User{ImageKey: []byte("key-a"), ProfileImageID: stringPointer("profile-a")}
	firstRequest := newProfileImageURLSigner()
	firstURL := firstRequest.profileImageURLAt(user, 1760000000)
	if firstURL == nil {
		t.Fatal("profileImageURLAt() returned nil for a profile image")
	}
	original := *firstURL
	*firstURL = "changed by caller"

	if got := firstRequest.profileImageURLAt(user, 1760000000); got == nil || *got != original {
		t.Fatalf("caller mutation changed cached URL: got %v, want %q", got, original)
	}
	secondRequest := newProfileImageURLSigner()
	if _, ok := secondRequest.urls[profileImageURLKey{signingKey: string(user.ImageKey), imageID: *user.ProfileImageID, exp: 1760000000}]; ok {
		t.Fatal("a new response signer inherited a previous request's cache")
	}
}

func BenchmarkRepeatedProfileImageURLs(b *testing.B) {
	user := User{ImageKey: []byte("benchmark-key"), ProfileImageID: stringPointer("profile-a")}
	const exp = 1760000000
	const repetitions = 30

	b.Run("request cache", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			signer := newProfileImageURLSigner()
			for range repetitions {
				signer.profileImageURLAt(user, exp)
			}
		}
	})
	b.Run("direct signing", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			for range repetitions {
				signedImageURLWithExpiration(user, *user.ProfileImageID, exp)
			}
		}
	})
}

func stringPointer(value string) *string {
	return &value
}
