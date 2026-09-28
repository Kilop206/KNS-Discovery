package discovery

import (
	"context"
	"errors"
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
	name       string
	expires    time.Time
	staleUntil time.Time
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
		entry, cached := identifier.cache[job.cacheKey]
		if cached && entry.name != "" && now.Before(entry.staleUntil) {
			observation.Names[job.key] = entry.name
		}
		if !cached || !now.Before(entry.expires) {
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
				mutex.Lock()
				now := identifier.now()
				entry := nameEntry{name: name, expires: now.Add(time.Minute)}
				if name != "" {
					entry.expires = now.Add(10 * time.Minute)
					entry.staleUntil = now.Add(30 * time.Minute)
				} else {
					var dnsError *net.DNSError
					notFound := errors.As(err, &dnsError) && dnsError.IsNotFound
					previous := identifier.cache[job.cacheKey]
					if err != nil && !notFound && now.Before(previous.staleUntil) {
						entry.name = previous.name
						entry.staleUntil = previous.staleUntil
					}
				}
				identifier.cache[job.cacheKey] = entry
				name = entry.name
				if name != "" {
					observation.Names[job.key] = name
				} else {
					delete(observation.Names, job.key)
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
