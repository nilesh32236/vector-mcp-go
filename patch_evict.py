import re

with open('internal/db/store.go', 'r') as f:
    content = f.read()

old_code = '''	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	// Simple eviction: if cache gets too big, clear it to prevent memory leaks
	if len(s.parsedCache) > 10000 {
		s.parsedCache = make(map[string][]string)
	}
	s.parsedCache[jsonStr] = arr
	return arr'''

new_code = '''	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	// Partial eviction: if cache gets too big, remove ~10% of entries to prevent thundering herd
	if len(s.parsedCache) > 10000 {
		evictCount := 1000
		for k := range s.parsedCache {
			delete(s.parsedCache, k)
			evictCount--
			if evictCount <= 0 {
				break
			}
		}
	}
	s.parsedCache[jsonStr] = arr
	return arr'''

content = content.replace(old_code, new_code)

with open('internal/db/store.go', 'w') as f:
    f.write(content)
