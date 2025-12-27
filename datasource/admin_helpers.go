package datasource

import "sort"

// SummarizeRouteConfig converts a RouteConfig into an AdminRouteInfo.
func SummarizeRouteConfig(key string, config *RouteConfig) AdminRouteInfo {
	info := AdminRouteInfo{Key: key}
	if config == nil {
		return info
	}

	info.RuleCount = len(config.Rules)
	if config.Upstream != "" {
		info.Upstream = config.Upstream
		info.Upstreams = append(info.Upstreams, config.Upstream)
	}

	seen := map[string]struct{}{}
	for _, addr := range info.Upstreams {
		seen[addr] = struct{}{}
	}

	for _, rule := range config.Rules {
		for _, u := range rule.Upstreams {
			if u.Address == "" {
				continue
			}
			if _, ok := seen[u.Address]; ok {
				continue
			}
			seen[u.Address] = struct{}{}
			info.Upstreams = append(info.Upstreams, u.Address)
		}
	}

	sort.Strings(info.Upstreams)
	return info
}
