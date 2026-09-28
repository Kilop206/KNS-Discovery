package discovery

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

func addressKey(index int, address netip.Addr) string {
	return strconv.Itoa(index) + ":" + address.String()
}

type nameEntry struct {
	name    string
	expires time.Time
}

// Identifier is reused between sequential collection attempts. Queries are
// bounded and cached, including negative answers; no port scan is performed.
type Identifier struct {
	lookup       func(context.Context, string) ([]string, error)
	now          func() time.Time
	cache        map[string]nameEntry
	budget       time.Duration
	queryTimeout time.Duration
	workers      int
}

func NewIdentifier() *Identifier {
	return &Identifier{lookup: net.DefaultResolver.LookupAddr, now: time.Now,
		cache: make(map[string]nameEntry), budget: 3 * time.Second,
		queryTimeout: 750 * time.Millisecond, workers: 8}
}

func (identifier *Identifier) Enrich(ctx context.Context, observation *Observation, selectedInterface string) {
	ctx, cancel := context.WithTimeout(ctx, identifier.budget)
	defer cancel()
	observation.Names = make(map[string]string)
	interfaces := make(map[int]Interface)
	for _, iface := range observation.Interfaces {
		if selectedInterface == "" || selectedInterface == iface.Name {
			interfaces[iface.Index] = iface
		}
	}
	type job struct{ key, cacheKey, address string }
	jobsByKey := make(map[string]job)
	add := func(index int, address netip.Addr, mac string) {
		iface, ok := interfaces[index]
		if !ok || !usableAddress(address) || address.IsLinkLocalUnicast() {
			return
		}
		local := false
		onLink := false
		for _, prefix := range iface.Prefixes {
			local = local || prefix.Addr() == address
			onLink = onLink || prefix.Contains(address)
		}
		if local || !onLink {
			return
		}
		key := addressKey(index, address)
		jobsByKey[key] = job{key, key + ":" + iface.Name + ":" + normalizedMAC(mac), address.String()}
	}
	for _, neighbor := range observation.Neighbors {
		switch strings.ToLower(neighbor.State) {
		case "reachable", "stale", "delay", "probe", "permanent":
			if normalizedMAC(neighbor.MAC) != "" {
				add(neighbor.Interface, neighbor.Address, neighbor.MAC)
			}
		}
	}
	for _, gateway := range observation.Gateways {
		if _, exists := jobsByKey[addressKey(gateway.Interface, gateway.Address)]; !exists {
			add(gateway.Interface, gateway.Address, "")
		}
	}
	now := identifier.now()
	active := make(map[string]bool)
	jobs := make([]job, 0, len(jobsByKey))
	for _, job := range jobsByKey {
		active[job.cacheKey] = true
		if entry, ok := identifier.cache[job.cacheKey]; ok && now.Before(entry.expires) {
			if entry.name != "" {
				observation.Names[job.key] = entry.name
			}
		} else {
			jobs = append(jobs, job)
		}
	}
	for key := range identifier.cache {
		if !active[key] {
			delete(identifier.cache, key)
		}
	}
	slices.SortFunc(jobs, func(a, b job) int { return strings.Compare(a.key, b.key) })
	queue := make(chan job, len(jobs))
	for _, job := range jobs {
		queue <- job
	}
	close(queue)
	var mutex sync.Mutex
	var workers sync.WaitGroup
	for range min(identifier.workers, len(jobs)) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for job := range queue {
				if ctx.Err() != nil {
					return
				}
				attempt, stop := context.WithTimeout(ctx, identifier.queryTimeout)
				names, err := identifier.lookup(attempt, job.address)
				stop()
				if ctx.Err() != nil {
					return
				}
				name := ""
				if err == nil {
					name = preferredName(names)
				}
				ttl := time.Minute
				if name != "" {
					ttl = 10 * time.Minute
				}
				mutex.Lock()
				identifier.cache[job.cacheKey] = nameEntry{name, identifier.now().Add(ttl)}
				if name != "" {
					observation.Names[job.key] = name
				}
				mutex.Unlock()
			}
		}()
	}
	workers.Wait()
}

func preferredName(names []string) string {
	valid := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(name), "."))
		if name == "" || len(name) > 253 {
			continue
		}
		if _, err := netip.ParseAddr(name); err == nil {
			continue
		}
		if strings.ContainsAny(name, "\r\n\t /\\") {
			continue
		}
		valid = append(valid, name)
	}
	slices.Sort(valid)
	if len(valid) == 0 {
		return ""
	}
	return valid[0]
}

// Hostnames are hints, not hardware fingerprints. Only explicit device words
// and common default host prefixes are recognized; ambiguous names stay unknown.
func typeFromHostname(name string) string {
	short := strings.Split(strings.ToLower(name), ".")[0]
	tokens := strings.FieldsFunc(short, func(r rune) bool { return r == '-' || r == '_' })
	for _, token := range tokens {
		switch token {
		case "printer", "laserjet", "officejet", "deskjet":
			return "printer"
		case "iphone", "android", "phone", "ipad":
			return "phone"
		case "desktop", "laptop", "macbook", "imac", "workstation":
			return "computer"
		case "nas", "server", "synology", "diskstation":
			return "server"
		case "chromecast", "roku", "smarttv":
			return "iot"
		}
	}
	return "unknown"
}
