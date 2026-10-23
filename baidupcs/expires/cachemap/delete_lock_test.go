package cachemap

import "testing"

func TestDeletePreservesHeldKeyLock(t *testing.T) {
	cache := &cacheUnit{}
	cache.LockKey("key")
	before, _ := cache.keyMap.Load("key")
	cache.Delete("key")
	after, ok := cache.keyMap.Load("key")
	if !ok || before != after {
		t.Fatal("deleting data replaced held key lock")
	}
	cache.UnlockKey("key")
	cache.LockKey("key")
	cache.UnlockKey("key")
}
