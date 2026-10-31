package isureal

import (
	"bytes"
	"crypto/sha256"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestImageSignatureCacheSeparatesSigningInputs(t *testing.T) {
	cache := newImageSignatureCache()
	key := bytes.Repeat([]byte{0x11}, sha256.Size)
	const imageID = "image-a"
	exp := time.Now().Add(time.Hour).Unix()

	want := cache.signature(key, imageID, exp)
	if got := cache.signature(key, imageID, exp); got != want {
		t.Fatalf("cache hit returned %x, want %x", got, want)
	}
	for _, input := range []struct {
		name string
		key  []byte
		id   string
		exp  int64
	}{
		{name: "different key", key: bytes.Repeat([]byte{0x22}, sha256.Size), id: imageID, exp: exp},
		{name: "different image", key: key, id: "image-b", exp: exp},
		{name: "different expiry", key: key, id: imageID, exp: exp + 1},
	} {
		t.Run(input.name, func(t *testing.T) {
			if got := cache.signature(input.key, input.id, input.exp); got == want {
				t.Fatal("different signing inputs reused the first signature")
			}
		})
	}
}

func TestImageSignatureCacheStoresOnlyVerifiedSignatures(t *testing.T) {
	cache := newImageSignatureCache()
	key := bytes.Repeat([]byte{0x33}, sha256.Size)
	const imageID = "image-c"
	exp := time.Now().Add(time.Hour).Unix()
	correct := computeImageSignature(key, imageID, exp)
	wrong := correct
	wrong[0] ^= 0xff

	if cache.verifyAndCache(key, imageID, exp, wrong[:]) {
		t.Fatal("invalid signature was accepted")
	}
	shard := cache.shardFor(imageSignatureCacheKey{signingKey: string(key), imageID: imageID, exp: exp})
	if len(shard.entries) != 0 {
		t.Fatal("invalid signature populated the cache")
	}
	if !cache.verifyAndCache(key, imageID, exp, correct[:]) {
		t.Fatal("valid signature was rejected")
	}
	cacheKey := imageSignatureCacheKey{signingKey: string(key), imageID: imageID, exp: exp}
	cached := shard.entries[cacheKey]
	if cached == nil {
		t.Fatal("valid signature was not cached")
	}
	if got := cached.Value.(imageSignatureCacheEntry).signature; got != correct {
		t.Fatalf("cached signature = %x, want %x", got, correct)
	}
	if cache.verifyAndCache(key, imageID, exp, wrong[:]) {
		t.Fatal("cached valid signature caused a different signature to be accepted")
	}
}

func TestImageSignatureCacheDoesNotRetainExpiredResults(t *testing.T) {
	cache := newImageSignatureCache()
	key := bytes.Repeat([]byte{0x44}, sha256.Size)
	const imageID = "expired-image"
	const exp = 1

	cache.signature(key, imageID, exp)
	shard := cache.shardFor(imageSignatureCacheKey{signingKey: string(key), imageID: imageID, exp: exp})
	if len(shard.entries) != 0 {
		t.Fatal("expired signature was retained")
	}
}

func TestImageSignatureCacheReturnsIndependentValuesForConcurrentHits(t *testing.T) {
	cache := newImageSignatureCache()
	key := bytes.Repeat([]byte{0x55}, sha256.Size)
	const imageID = "concurrent-image"
	exp := time.Now().Add(time.Hour).Unix()
	want := computeImageSignature(key, imageID, exp)
	if got := cache.signature(key, imageID, exp); got != want {
		t.Fatalf("initial signature = %x, want %x", got, want)
	}

	first := cache.signature(key, imageID, exp)
	first[0] ^= 0xff
	if got := cache.signature(key, imageID, exp); got != want {
		t.Fatalf("mutating a returned signature changed the cache: got %x, want %x", got, want)
	}

	const callers = 16
	var wait sync.WaitGroup
	signatures := make(chan [sha256.Size]byte, callers)
	for range callers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			signatures <- cache.signature(key, imageID, exp)
		}()
	}
	wait.Wait()
	close(signatures)
	for got := range signatures {
		if got != want {
			t.Fatalf("concurrent cache hit = %x, want %x", got, want)
		}
	}
}

func TestImageSignatureCacheCapacityAndClear(t *testing.T) {
	cache := newImageSignatureCache()
	cache.signature(bytes.Repeat([]byte{0x66}, sha256.Size), "clear-image", time.Now().Add(time.Hour).Unix())
	var target *imageSignatureCacheShard
	keys := make([]imageSignatureCacheKey, 0, imageSignatureCacheShardSize+1)
	for candidate := 0; len(keys) < imageSignatureCacheShardSize+1; candidate++ {
		key := imageSignatureCacheKey{signingKey: "key", imageID: strconv.Itoa(candidate), exp: 1760000000}
		shard := cache.shardFor(key)
		if target == nil {
			target = shard
		}
		if shard != target {
			continue
		}
		keys = append(keys, key)
	}
	for i, key := range keys {
		var signature [sha256.Size]byte
		signature[0] = byte(i)
		target.insert(key, signature)
	}
	if got := target.lru.Len(); got != imageSignatureCacheShardSize {
		t.Fatalf("shard size = %d, want %d", got, imageSignatureCacheShardSize)
	}
	if _, ok := target.entries[keys[0]]; ok {
		t.Fatal("least recently used signature survived capacity eviction")
	}

	cache.clear()
	for i := range cache.shards {
		if got := cache.shards[i].lru.Len(); got != 0 {
			t.Fatalf("shard %d retains %d entries after clear", i, got)
		}
	}
}
