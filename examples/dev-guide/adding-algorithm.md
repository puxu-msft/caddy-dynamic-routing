# Adding a New Load Balancing Algorithm

This document contains the algorithm-extension example referenced from root docs.

## Where it lives

- Algorithms are implemented in the selector under `matcher/`.

## Outline

1. Add a new algorithm constant.
2. Extend the selector switch to route to your implementation.
3. Add unit tests (including concurrent tests where appropriate).

## Example (illustrative)

```go
const (
	AlgorithmMyNew = "mynew"
)

func (s *UpstreamSelector) Select(upstreams []datasource.Upstream, req *http.Request, algorithm, algorithmKey string) *datasource.Upstream {
	switch algorithm {
	case AlgorithmMyNew:
		return s.selectMyNew(upstreams, req, algorithmKey)
	default:
		return s.selectWeightedRandom(upstreams)
	}
}
```
