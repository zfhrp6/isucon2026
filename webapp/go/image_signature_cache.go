package isureal

import (
	"container/list"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	imageSignatureCacheShardCount = 32
	imageSignatureCacheShardSize  = 256
)

type imageSignatureCacheKey struct {
	signingKey string
	imageID    string
	exp        int64
}

type imageSignatureCacheEntry struct {
	key       imageSignatureCacheKey
	signature [sha256.Size]byte
}

type imageSignatureCacheShard struct {
	mu      sync.Mutex
	entries map[imageSignatureCacheKey]*list.Element
	lru     *list.List
}

type imageSignatureCache struct {
	shards     [imageSignatureCacheShardCount]imageSignatureCacheShard
	generation atomic.Uint64
}

var processImageSignatures = newImageSignatureCache()

func newImageSignatureCache() *imageSignatureCache {
	cache := &imageSignatureCache{}
	for i := range cache.shards {
		cache.shards[i] = imageSignatureCacheShard{
			entries: make(map[imageSignatureCacheKey]*list.Element),
			lru:     list.New(),
		}
	}
	return cache
}

func (cache *imageSignatureCache) signature(key []byte, imageID string, exp int64) [sha256.Size]byte {
	generation := cache.generation.Load()
	cacheKey := imageSignatureCacheKey{signingKey: string(key), imageID: imageID, exp: exp}
	shard := cache.shardFor(cacheKey)
	shard.mu.Lock()
	defer shard.mu.Unlock()

	if cached := shard.entries[cacheKey]; cached != nil {
		entry := cached.Value.(imageSignatureCacheEntry)
		if exp > time.Now().Unix() {
			shard.lru.MoveToFront(cached)
			return entry.signature
		}
		shard.remove(cached)
	}

	// Keep the shard lock through a miss calculation so concurrent requests for
	// the same signing inputs cannot each repeat the 1000 HMAC rounds.
	signature := computeImageSignature(key, imageID, exp)
	if cache.generation.Load() == generation && exp > time.Now().Unix() {
		shard.insert(cacheKey, signature)
	}
	return signature
}

func (cache *imageSignatureCache) verifyAndCache(key []byte, imageID string, exp int64, received []byte) bool {
	generation := cache.generation.Load()
	cacheKey := imageSignatureCacheKey{signingKey: string(key), imageID: imageID, exp: exp}
	shard := cache.shardFor(cacheKey)
	shard.mu.Lock()

	if cached := shard.entries[cacheKey]; cached != nil {
		entry := cached.Value.(imageSignatureCacheEntry)
		if exp > time.Now().Unix() {
			valid := hmac.Equal(received, entry.signature[:])
			if valid {
				shard.lru.MoveToFront(cached)
			}
			shard.mu.Unlock()
			return valid
		}
		shard.remove(cached)
	}
	shard.mu.Unlock()

	// Verification misses compute without holding a shard lock: an attacker can
	// submit arbitrary future expirations and signatures, so sharing the lock
	// would let invalid requests stall valid cache users. Concurrent misses may
	// duplicate this work; URL-generation misses are coalesced separately.
	signature := computeImageSignature(key, imageID, exp)
	if !hmac.Equal(received, signature[:]) {
		return false
	}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	// Only successful verifications enter the cache, and a clear that happened
	// during computation prevents this older request from repopulating it.
	if cache.generation.Load() == generation && exp > time.Now().Unix() {
		shard.insert(cacheKey, signature)
	}
	return true
}

func (cache *imageSignatureCache) clear() {
	for i := range cache.shards {
		cache.shards[i].mu.Lock()
	}
	for i := range cache.shards {
		shard := &cache.shards[i]
		shard.entries = make(map[imageSignatureCacheKey]*list.Element)
		shard.lru.Init()
	}
	cache.generation.Add(1)
	for i := len(cache.shards) - 1; i >= 0; i-- {
		cache.shards[i].mu.Unlock()
	}
}

func (cache *imageSignatureCache) shardFor(key imageSignatureCacheKey) *imageSignatureCacheShard {
	hasher := fnv.New64a()
	var size [8]byte
	for _, value := range []string{key.signingKey, key.imageID} {
		binary.LittleEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hasher.Write(size[:])
		_, _ = hasher.Write([]byte(value))
	}
	binary.LittleEndian.PutUint64(size[:], uint64(key.exp))
	_, _ = hasher.Write(size[:])
	return &cache.shards[hasher.Sum64()%uint64(len(cache.shards))]
}

func (shard *imageSignatureCacheShard) insert(key imageSignatureCacheKey, signature [sha256.Size]byte) {
	if existing := shard.entries[key]; existing != nil {
		existing.Value = imageSignatureCacheEntry{key: key, signature: signature}
		shard.lru.MoveToFront(existing)
		return
	}
	if shard.lru.Len() >= imageSignatureCacheShardSize {
		shard.remove(shard.lru.Back())
	}
	element := shard.lru.PushFront(imageSignatureCacheEntry{key: key, signature: signature})
	shard.entries[key] = element
}

func (shard *imageSignatureCacheShard) remove(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(imageSignatureCacheEntry)
	delete(shard.entries, entry.key)
	shard.lru.Remove(element)
}
